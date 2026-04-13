import { describe, test, expect, beforeAll, afterAll, beforeEach, afterEach } from 'vitest';
import puppeteer, { Browser, Page, BrowserContext } from 'puppeteer';

process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';

const FRONTEND_PORT = 3000;
const BACKEND_PORT = 3001;
const CONTROL_PORT = 13100;
const FRONTEND_COOKIE = `__mdp_upstream_${FRONTEND_PORT}`;
const BACKEND_COOKIE = `__mdp_upstream_${BACKEND_PORT}`;
const FRONTEND_BASE = `https://localhost:${FRONTEND_PORT}`;
const BACKEND_BASE = `https://localhost:${BACKEND_PORT}`;
const CONTROL_URL = `http://127.0.0.1:${CONTROL_PORT}`;

const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));

// ─── helpers ─────────────────────────────────────────────────────────────────

interface ProxyInfo {
  port: number;
  label: string;
  cookieName: string;
  default: string;
  servers: { name: string; port: number; pid: number; group: string }[];
}

async function getProxies(): Promise<ProxyInfo[]> {
  const resp = await fetch(`${CONTROL_URL}/__mdp/proxies`);
  return resp.json();
}

async function getGroups(): Promise<Record<string, string[]>> {
  const resp = await fetch(`${CONTROL_URL}/__mdp/groups`);
  return resp.json();
}

function proxyByPort(proxies: ProxyInfo[], port: number): ProxyInfo | undefined {
  return proxies.find(p => p.port === port);
}

function serverNamesOnPort(proxies: ProxyInfo[], port: number): string[] {
  const proxy = proxyByPort(proxies, port);
  return proxy ? proxy.servers.map(s => s.name).sort() : [];
}

// ─── browser lifecycle ──────────────────────────────────────────────────────

let browser: Browser;
let context: BrowserContext;
let page: Page;

beforeAll(async () => {
  browser = await puppeteer.launch({
    headless: !!process.env.CI,
    args: ['--ignore-certificate-errors', '--no-sandbox'],
  });
});

afterAll(async () => {
  await browser?.close();
});

beforeEach(async () => {
  context = await browser.createBrowserContext();
  page = await context.newPage();
});

afterEach(async () => {
  await page?.close();
  await context?.close();
});

// ─── orchestrator control API ────────────────────────────────────────────────

describe('control API', () => {
  test('health endpoint returns ok', async () => {
    const resp = await fetch(`${CONTROL_URL}/__mdp/health`);
    expect(resp.ok).toBe(true);
    const body = await resp.json();
    expect(body.ok).toBe(true);
    expect(body.proxies).toBeGreaterThanOrEqual(2);
  });

  test('lists both proxy instances', async () => {
    const proxies = await getProxies();
    const ports = proxies.map(p => p.port).sort();
    expect(ports).toContain(FRONTEND_PORT);
    expect(ports).toContain(BACKEND_PORT);
  });

  test('frontend proxy has servers registered', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT);
    expect(frontend).toBeTruthy();
    expect(frontend!.servers.length).toBeGreaterThanOrEqual(5);
  });

  test('backend proxy has servers registered', async () => {
    const proxies = await getProxies();
    const backend = proxyByPort(proxies, BACKEND_PORT);
    expect(backend).toBeTruthy();
    expect(backend!.servers.length).toBeGreaterThanOrEqual(2);
  });

  test('each proxy has port-specific cookie name', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT);
    const backend = proxyByPort(proxies, BACKEND_PORT);
    expect(frontend!.cookieName).toBe(FRONTEND_COOKIE);
    expect(backend!.cookieName).toBe(BACKEND_COOKIE);
  });

  test('services endpoint returns managed services', async () => {
    const resp = await fetch(`${CONTROL_URL}/__mdp/services`);
    expect(resp.ok).toBe(true);
    const services = await resp.json();
    expect(Array.isArray(services)).toBe(true);
  });
});

// ─── groups ──────────────────────────────────────────────────────────────────

describe('groups', () => {
  test('dev and staging groups exist', async () => {
    const groups = await getGroups();
    expect(Object.keys(groups)).toContain('dev');
    expect(Object.keys(groups)).toContain('staging');
  });

  test('dev group spans both proxies', async () => {
    const groups = await getGroups();
    const devMembers = groups['dev'];
    expect(devMembers.some(m => m.includes('vite'))).toBe(true);
    expect(devMembers.some(m => m.includes('echo'))).toBe(true);
  });

  test('staging group spans both proxies', async () => {
    const groups = await getGroups();
    const stagingMembers = groups['staging'];
    expect(stagingMembers.some(m => m.includes('next'))).toBe(true);
    expect(stagingMembers.some(m => m.includes('docker'))).toBe(true);
  });

  test('explicitly configured groups have at most one service per proxy port', async () => {
    const proxies = await getProxies();
    const groups = await getGroups();

    for (const [groupName, members] of Object.entries(groups)) {
      if (groupName === 'dev' || groupName === 'staging') {
        const portCounts = new Map<number, number>();
        for (const memberName of members) {
          for (const proxy of proxies) {
            if (proxy.servers.some(s => s.name === memberName)) {
              portCounts.set(proxy.port, (portCounts.get(proxy.port) || 0) + 1);
            }
          }
        }
        for (const [port, count] of portCounts) {
          expect(count, `group ${groupName} has ${count} services on :${port}`).toBe(1);
        }
      }
    }
  });
});

// ─── group switching ─────────────────────────────────────────────────────────

describe('group switching', () => {
  test('switching to dev sets defaults on both proxies', async () => {
    const resp = await fetch(`${CONTROL_URL}/__mdp/groups/dev/switch`, { method: 'POST' });
    expect(resp.ok).toBe(true);

    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const backend = proxyByPort(proxies, BACKEND_PORT)!;

    expect(frontend.default).toContain('vite');
    expect(backend.default).toContain('echo');
  });

  test('switching to staging changes defaults on both proxies', async () => {
    const resp = await fetch(`${CONTROL_URL}/__mdp/groups/staging/switch`, { method: 'POST' });
    expect(resp.ok).toBe(true);

    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const backend = proxyByPort(proxies, BACKEND_PORT)!;

    expect(frontend.default).toContain('next');
    expect(backend.default).toContain('docker');
  });

  test('switching nonexistent group returns error', async () => {
    const resp = await fetch(`${CONTROL_URL}/__mdp/groups/doesnotexist/switch`, { method: 'POST' });
    expect(resp.ok).toBe(false);
  });
});

// ─── per-proxy default management ────────────────────────────────────────────

describe('default upstream', () => {
  test('set and get default on frontend proxy', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const target = frontend.servers[0].name;

    const setResp = await fetch(
      `${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default/${encodeURIComponent(target)}`,
      { method: 'POST' },
    );
    expect(setResp.ok).toBe(true);

    const updated = await getProxies();
    expect(proxyByPort(updated, FRONTEND_PORT)!.default).toBe(target);
  });

  test('clear default on frontend proxy', async () => {
    const resp = await fetch(
      `${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default`,
      { method: 'DELETE' },
    );
    expect(resp.ok).toBe(true);

    const updated = await getProxies();
    expect(proxyByPort(updated, FRONTEND_PORT)!.default).toBe('');
  });

  test('set default on backend proxy independently', async () => {
    const proxies = await getProxies();
    const backend = proxyByPort(proxies, BACKEND_PORT)!;
    const target = backend.servers[0].name;

    await fetch(
      `${CONTROL_URL}/__mdp/proxies/${BACKEND_PORT}/default/${encodeURIComponent(target)}`,
      { method: 'POST' },
    );

    const updated = await getProxies();
    expect(proxyByPort(updated, BACKEND_PORT)!.default).toBe(target);

    const frontendBefore = proxyByPort(proxies, FRONTEND_PORT)!.default;
    const frontendAfter = proxyByPort(updated, FRONTEND_PORT)!.default;
    expect(frontendAfter).toBe(frontendBefore);
  });
});

// ─── deregister ──────────────────────────────────────────────────────────────

describe('deregister', () => {
  test('register and deregister a temporary server', async () => {
    const regResp = await fetch(`${CONTROL_URL}/__mdp/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'test/temp-server', port: 59999, proxyPort: FRONTEND_PORT, group: 'test' }),
    });
    expect(regResp.ok).toBe(true);

    let proxies = await getProxies();
    let names = serverNamesOnPort(proxies, FRONTEND_PORT);
    expect(names).toContain('test/temp-server');

    const deregResp = await fetch(
      `${CONTROL_URL}/__mdp/register/test/temp-server`,
      { method: 'DELETE' },
    );
    expect(deregResp.ok).toBe(true);
    const body = await deregResp.json();
    expect(body.deleted).toBe(true);

    proxies = await getProxies();
    names = serverNamesOnPort(proxies, FRONTEND_PORT);
    expect(names).not.toContain('test/temp-server');
  });

  test('deregister nonexistent server returns deleted=false', async () => {
    const resp = await fetch(
      `${CONTROL_URL}/__mdp/register/nonexistent/server`,
      { method: 'DELETE' },
    );
    expect(resp.ok).toBe(true);
    const body = await resp.json();
    expect(body.deleted).toBe(false);
  });
});

// ─── proxy-level endpoints ───────────────────────────────────────────────────

describe('proxy health and config', () => {
  test('frontend proxy health endpoint', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/health`);
    expect(resp.ok).toBe(true);
    const body = await resp.json();
    expect(body.ok).toBe(true);
    expect(body.servers).toBeGreaterThan(0);
  });

  test('frontend proxy config returns correct cookie and port', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/config`);
    expect(resp.ok).toBe(true);
    const config = await resp.json();
    expect(config.cookieName).toBe(FRONTEND_COOKIE);
    expect(config.port).toBe(FRONTEND_PORT);
  });

  test('frontend config lists backend as sibling', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/config`);
    const config = await resp.json();
    const backendSibling = (config.siblings || []).find(
      (s: { port: number }) => s.port === BACKEND_PORT,
    );
    expect(backendSibling).toBeTruthy();
    expect(backendSibling.cookieName).toBe(BACKEND_COOKIE);
  });

  test('frontend config includes groups', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/config`);
    const config = await resp.json();
    expect(config.groups).toBeTruthy();
    expect(Object.keys(config.groups)).toContain('dev');
    expect(Object.keys(config.groups)).toContain('staging');
  });

  test('backend proxy health endpoint', async () => {
    const resp = await fetch(`${BACKEND_BASE}/__mdp/health`);
    if (resp.ok) {
      const body = await resp.json();
      expect(body.ok).toBe(true);
    }
  });

  test('backend proxy config uses its own cookie', async () => {
    const resp = await fetch(`${BACKEND_BASE}/__mdp/config`);
    if (resp.ok) {
      const config = await resp.json();
      expect(config.cookieName).toBe(BACKEND_COOKIE);
      expect(config.port).toBe(BACKEND_PORT);
    }
  });
});

// ─── switch page ─────────────────────────────────────────────────────────────

describe('switch page', () => {
  test('lists servers with switch buttons', async () => {
    await page.goto(`${FRONTEND_BASE}/__mdp/switch`, { waitUntil: 'load' });
    const h1Text = await page.$eval('h1', el => el.textContent);
    expect(h1Text).toBe('Dev Server Switcher');
    const buttonCount = await page.$$eval('.btn', els => els.length);
    expect(buttonCount).toBeGreaterThan(0);
  });

  test('switch button sets cookie and redirects to app', async () => {
    await fetch(`${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default`, { method: 'DELETE' });

    await page.goto(`${FRONTEND_BASE}/__mdp/switch`, { waitUntil: 'load' });
    await Promise.all([
      page.waitForNavigation({ waitUntil: 'load' }),
      page.click('form .btn'),
    ]);

    expect(page.url()).not.toContain('__mdp/switch');

    const cookies = await page.cookies();
    expect(cookies.find(c => c.name === FRONTEND_COOKIE)).toBeTruthy();

    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    expect(frontend.default).toBeTruthy();
  });

  test('theme toggle works', async () => {
    await page.goto(`${FRONTEND_BASE}/__mdp/switch`, { waitUntil: 'load' });

    await page.click('#theme-light');
    await page.waitForFunction(() => document.body.classList.contains('light'));

    await page.click('#theme-dark');
    await page.waitForFunction(() => document.body.classList.contains('dark'));

    await page.click('#theme-auto');
    await page.waitForFunction(
      () => !document.body.classList.contains('light') && !document.body.classList.contains('dark'),
    );
  });
});

// ─── widget ──────────────────────────────────────────────────────────────────

describe('widget', () => {
  test('widget JS is served with correct content', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/widget.js`);
    expect(resp.ok).toBe(true);
    expect(resp.headers.get('content-type')).toContain('javascript');
    const body = await resp.text();
    expect(body).toContain('attachShadow');
    expect(body).toContain('__mdp_upstream');
  });

  test('widget pill appears on proxied page', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const firstName = frontend.servers[0].name;

    await page.setCookie({
      name: FRONTEND_COOKIE,
      value: encodeURIComponent(firstName),
      domain: 'localhost',
      path: '/',
    });

    await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'load' });
    await sleep(2000);
    const widgetExists = await page.evaluate(
      () => document.getElementById('__mdp-widget-host') !== null,
    );
    expect(widgetExists).toBe(true);
  });
});

// ─── server routing ──────────────────────────────────────────────────────────

describe('server routing', () => {
  test('redirects to switch page with no cookie and no default', async () => {
    await fetch(`${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default`, { method: 'DELETE' });
    await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'domcontentloaded' });
    expect(page.url()).toMatch(/__mdp\/switch/);
  });

  test('every frontend server is reachable through the proxy', async () => {
    const proxies = await getProxies();
    const names = serverNamesOnPort(proxies, FRONTEND_PORT);

    for (const name of names) {
      await page.setCookie({
        name: FRONTEND_COOKIE,
        value: encodeURIComponent(name),
        domain: 'localhost',
        path: '/',
      });

      let resp = await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'load' });
      for (let attempt = 0; !resp?.ok() && attempt < 3; attempt++) {
        await sleep(2000);
        resp = await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'load' });
      }
      expect(resp?.ok(), `server ${name} returned ${resp?.status()}`).toBe(true);
      const html = await page.content();
      expect(html.length, `server ${name} returned empty page`).toBeGreaterThan(100);
    }
  });

  test('widget is injected on every frontend server', async () => {
    const proxies = await getProxies();
    const names = serverNamesOnPort(proxies, FRONTEND_PORT);

    for (const name of names) {
      await page.setCookie({
        name: FRONTEND_COOKIE,
        value: encodeURIComponent(name),
        domain: 'localhost',
        path: '/',
      });

      await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'load' });
      await sleep(2000);
      const injected = await page.evaluate(() => {
        return document.querySelector('script[src="/__mdp/widget.js"]') !== null
          || document.getElementById('__mdp-widget-host') !== null;
      });
      expect(injected, `widget not injected on ${name}`).toBe(true);
    }
  });

  test('different servers produce different content', async () => {
    const proxies = await getProxies();
    const names = serverNamesOnPort(proxies, FRONTEND_PORT);
    if (names.length < 2) return;

    const contents: Record<string, string> = {};
    for (const name of names) {
      await page.setCookie({
        name: FRONTEND_COOKIE,
        value: encodeURIComponent(name),
        domain: 'localhost',
        path: '/',
      });
      await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'load' });
      contents[name] = await page.content();
    }

    const uniqueContents = new Set(Object.values(contents));
    expect(
      uniqueContents.size,
      `expected ${names.length} unique pages but got ${uniqueContents.size}`,
    ).toBe(names.length);
  });

  test('default upstream routes without cookie', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const target = frontend.servers[0].name;

    await fetch(
      `${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default/${encodeURIComponent(target)}`,
      { method: 'POST' },
    );

    const resp = await fetch(`${FRONTEND_BASE}/`, {
      headers: { Accept: 'text/html' },
      redirect: 'manual',
    });
    expect(resp.status).toBeLessThan(400);

    await fetch(`${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default`, { method: 'DELETE' });
  });
});

// ─── cookie isolation ────────────────────────────────────────────────────────

describe('cookie isolation', () => {
  test('frontend and backend cookies are independent', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const backend = proxyByPort(proxies, BACKEND_PORT)!;

    expect(frontend.cookieName).not.toBe(backend.cookieName);
    expect(frontend.cookieName).toContain(String(FRONTEND_PORT));
    expect(backend.cookieName).toContain(String(BACKEND_PORT));
  });

  test('setting default on one proxy does not affect the other', async () => {
    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const backend = proxyByPort(proxies, BACKEND_PORT)!;

    await fetch(
      `${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default/${encodeURIComponent(frontend.servers[0].name)}`,
      { method: 'POST' },
    );
    await fetch(
      `${CONTROL_URL}/__mdp/proxies/${BACKEND_PORT}/default/${encodeURIComponent(backend.servers[0].name)}`,
      { method: 'POST' },
    );

    if (frontend.servers.length > 1) {
      await fetch(
        `${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default/${encodeURIComponent(frontend.servers[1].name)}`,
        { method: 'POST' },
      );
      const updated = await getProxies();
      expect(proxyByPort(updated, FRONTEND_PORT)!.default).toBe(frontend.servers[1].name);
      expect(proxyByPort(updated, BACKEND_PORT)!.default).toBe(backend.servers[0].name);
    }

    await fetch(`${CONTROL_URL}/__mdp/proxies/${FRONTEND_PORT}/default`, { method: 'DELETE' });
    await fetch(`${CONTROL_URL}/__mdp/proxies/${BACKEND_PORT}/default`, { method: 'DELETE' });
  });
});

// ─── CORS ────────────────────────────────────────────────────────────────────

describe('CORS', () => {
  test('__mdp endpoints return CORS headers', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/health`, {
      headers: { Origin: 'http://example.com' },
    });
    expect(resp.ok).toBe(true);
    expect(resp.headers.get('access-control-allow-origin')).toBeTruthy();
  });

  test('OPTIONS preflight returns no-content', async () => {
    const resp = await fetch(`${FRONTEND_BASE}/__mdp/health`, {
      method: 'OPTIONS',
      headers: {
        Origin: 'http://example.com',
        'Access-Control-Request-Method': 'POST',
      },
    });
    expect(resp.status).toBe(204);
    expect(resp.headers.get('access-control-allow-methods')).toContain('POST');
  });
});

// ─── stress ──────────────────────────────────────────────────────────────────

describe('stress', () => {
  test('rapid cookie-based switching', async () => {
    const proxies = await getProxies();
    const names = serverNamesOnPort(proxies, FRONTEND_PORT);
    if (names.length < 2) return;

    for (let i = 0; i < 10; i++) {
      const target = names[i % names.length];
      await page.setCookie({
        name: FRONTEND_COOKIE,
        value: encodeURIComponent(target),
        domain: 'localhost',
        path: '/',
      });
      const resp = await page.goto(`${FRONTEND_BASE}/`, { waitUntil: 'load' });
      const status = resp?.status() ?? 0;
      expect(status, `rapid switch #${i} to ${target} got ${status}`).toBeLessThan(502);
    }
  });

  test('rapid group switching via control API', async () => {
    const groups = await getGroups();
    const groupNames = Object.keys(groups);
    if (groupNames.length < 2) return;

    for (let i = 0; i < 10; i++) {
      const group = groupNames[i % groupNames.length];
      const resp = await fetch(`${CONTROL_URL}/__mdp/groups/${group}/switch`, { method: 'POST' });
      expect(resp.ok, `group switch #${i} to ${group} failed`).toBe(true);
    }

    const proxies = await getProxies();
    const frontend = proxyByPort(proxies, FRONTEND_PORT)!;
    const backend = proxyByPort(proxies, BACKEND_PORT)!;
    expect(frontend.default).toBeTruthy();
    expect(backend.default).toBeTruthy();
  });

  test('concurrent API requests', async () => {
    const results = await Promise.all([
      fetch(`${CONTROL_URL}/__mdp/health`),
      fetch(`${CONTROL_URL}/__mdp/proxies`),
      fetch(`${CONTROL_URL}/__mdp/groups`),
      fetch(`${CONTROL_URL}/__mdp/services`),
      fetch(`${FRONTEND_BASE}/__mdp/health`),
      fetch(`${FRONTEND_BASE}/__mdp/config`),
      fetch(`${FRONTEND_BASE}/__mdp/widget.js`),
    ]);
    for (const resp of results) {
      expect(resp.ok).toBe(true);
    }
  });

  test('register, switch, deregister cycle', async () => {
    for (let i = 0; i < 5; i++) {
      const name = `stress/temp-${i}`;
      await fetch(`${CONTROL_URL}/__mdp/register`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, port: 59900 + i, proxyPort: FRONTEND_PORT, group: 'stress' }),
      });
    }

    const resp = await fetch(`${CONTROL_URL}/__mdp/groups/stress/switch`, { method: 'POST' });
    expect(resp.ok).toBe(true);

    for (let i = 0; i < 5; i++) {
      await fetch(`${CONTROL_URL}/__mdp/register/stress/temp-${i}`, { method: 'DELETE' });
    }

    const proxies = await getProxies();
    const names = serverNamesOnPort(proxies, FRONTEND_PORT);
    for (let i = 0; i < 5; i++) {
      expect(names).not.toContain(`stress/temp-${i}`);
    }
  });
});
