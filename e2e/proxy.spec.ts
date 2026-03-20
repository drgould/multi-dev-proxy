import { test, expect, APIRequestContext } from '@playwright/test';

type ServerMap = Record<string, Record<string, { port: number; pid: number }>>;

async function getServers(request: APIRequestContext): Promise<ServerMap> {
  const resp = await request.get('/__mdp/servers');
  return resp.json();
}

function allServerNames(servers: ServerMap): string[] {
  const names: string[] = [];
  for (const repo of Object.keys(servers).sort()) {
    for (const name of Object.keys(servers[repo]).sort()) {
      names.push(name);
    }
  }
  return names;
}

test.describe('proxy health', () => {
  test('health endpoint returns ok', async ({ request }) => {
    const resp = await request.get('/__mdp/health');
    expect(resp.ok()).toBe(true);
    const body = await resp.json();
    expect(body.ok).toBe(true);
    expect(body.servers).toBeGreaterThan(0);
  });

  test('all expected servers are registered', async ({ request }) => {
    const servers = await getServers(request);
    const names = allServerNames(servers);

    const expected = [
      'testbed/go-websocket',
      'testbed/vite-ts',
      'frontend-app/nextjs',
      'frontend-app/vue',
      'testbed/svelte',
    ];

    for (const name of expected) {
      expect(names, `missing server: ${name}`).toContain(name);
    }
  });

  test('servers are grouped by repo', async ({ request }) => {
    const servers = await getServers(request);
    const repos = Object.keys(servers).sort();

    expect(repos).toContain('testbed');
    expect(repos).toContain('frontend-app');

    expect(Object.keys(servers['testbed'])).toContain('testbed/go-websocket');
    expect(Object.keys(servers['testbed'])).toContain('testbed/vite-ts');
    expect(Object.keys(servers['testbed'])).toContain('testbed/svelte');
    expect(Object.keys(servers['frontend-app'])).toContain('frontend-app/nextjs');
    expect(Object.keys(servers['frontend-app'])).toContain('frontend-app/vue');
  });
});

test.describe('switch page', () => {
  test('lists all servers with switch buttons', async ({ page, request }) => {
    const servers = await getServers(request);
    const names = allServerNames(servers);

    await page.goto('/__mdp/switch');
    await expect(page.locator('h1')).toHaveText('Dev Server Switcher');

    const buttons = await page.locator('.btn').count();
    expect(buttons).toBe(names.length);

    for (const name of names) {
      const branch = name.split('/').pop()!;
      await expect(page.locator('td', { hasText: branch })).toBeVisible();
    }
  });

  test('shows all repo group headings', async ({ page, request }) => {
    const servers = await getServers(request);
    const repos = Object.keys(servers).sort();

    await page.goto('/__mdp/switch');

    for (const repo of repos) {
      await expect(page.locator('.repo-name', { hasText: repo })).toBeVisible();
    }
  });

  test('theme toggle works', async ({ page }) => {
    await page.goto('/__mdp/switch');

    await page.locator('#theme-light').click();
    await expect(page.locator('body')).toHaveClass(/light/);

    await page.locator('#theme-dark').click();
    await expect(page.locator('body')).toHaveClass(/dark/);

    await page.locator('#theme-auto').click();
    await expect(page.locator('body')).not.toHaveClass(/light/);
    await expect(page.locator('body')).not.toHaveClass(/dark/);
  });

  test('switch button sets cookie and redirects', async ({ page, context }) => {
    await page.goto('/__mdp/switch');
    await page.locator('.btn').first().click();
    await page.waitForURL(url => !url.pathname.includes('__mdp/switch'));
    const cookies = await context.cookies();
    expect(cookies.find(c => c.name === '__mdp_upstream')).toBeTruthy();
  });
});

test.describe('widget', () => {
  test('widget JS is served with correct content', async ({ request }) => {
    const resp = await request.get('/__mdp/widget.js');
    expect(resp.ok()).toBe(true);
    expect(resp.headers()['content-type']).toContain('javascript');
    const body = await resp.text();
    expect(body).toContain('attachShadow');
    expect(body).toContain('__mdp_upstream');
    expect(body).toContain('__mdp/servers');
  });

  test('widget pill appears on proxied page', async ({ page, context, request }) => {
    const servers = await getServers(request);
    const firstName = allServerNames(servers)[0];

    await context.addCookies([{
      name: '__mdp_upstream',
      value: encodeURIComponent(firstName),
      domain: 'localhost',
      path: '/',
    }]);

    await page.goto('/');
    await page.waitForTimeout(2000);
    await expect(page.locator('#__mdp-widget-host')).toBeAttached();
  });
});

test.describe('server routing', () => {
  test('redirects to switch page with no cookie', async ({ page }) => {
    await page.goto('/', { waitUntil: 'commit' });
    await expect(page).toHaveURL(/\/__mdp\/switch/);
  });

  test('every registered server is reachable through the proxy', async ({ page, context, request }) => {
    const servers = await getServers(request);
    const names = allServerNames(servers);

    for (const name of names) {
      await context.addCookies([{
        name: '__mdp_upstream',
        value: encodeURIComponent(name),
        domain: 'localhost',
        path: '/',
      }]);

      let resp = await page.goto('/');
      if (!resp?.ok()) {
        await page.waitForTimeout(1000);
        resp = await page.goto('/');
      }
      expect(resp?.ok(), `server ${name} returned ${resp?.status()}`).toBe(true);

      const html = await page.content();
      expect(html.length, `server ${name} returned empty page`).toBeGreaterThan(100);
    }
  });

  test('widget is injected on every server', async ({ page, context, request }) => {
    const servers = await getServers(request);
    const names = allServerNames(servers);

    for (const name of names) {
      await context.addCookies([{
        name: '__mdp_upstream',
        value: encodeURIComponent(name),
        domain: 'localhost',
        path: '/',
      }]);

      await page.goto('/');
      await page.waitForTimeout(2000);

      const injected = await page.evaluate(() => {
        return document.querySelector('script[src="/__mdp/widget.js"]') !== null
          || document.getElementById('__mdp-widget-host') !== null;
      });
      expect(injected, `widget not injected on ${name}`).toBe(true);
    }
  });

  test('switching produces different content per server', async ({ page, context, request }) => {
    const servers = await getServers(request);
    const names = allServerNames(servers);
    if (names.length < 2) return;

    const contents: Record<string, string> = {};
    for (const name of names) {
      await context.addCookies([{
        name: '__mdp_upstream',
        value: encodeURIComponent(name),
        domain: 'localhost',
        path: '/',
      }]);

      await page.goto('/');
      contents[name] = await page.content();
    }

    const uniqueContents = new Set(Object.values(contents));
    expect(
      uniqueContents.size,
      `expected ${names.length} unique pages but got ${uniqueContents.size}`
    ).toBe(names.length);
  });
});
