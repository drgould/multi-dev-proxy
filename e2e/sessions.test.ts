import { describe, test, expect, beforeAll, afterAll } from 'vitest';
import { execSync, spawn, ChildProcess } from 'child_process';
import { join } from 'path';
import { createServer } from 'net';

const PROJECT_ROOT = join(__dirname, '..');
const BINARY = join(PROJECT_ROOT, 'mdp');
const PROXY_PORT = 19500; // arbitrary high port for test proxy registrations

// ─── helpers ─────────────────────────────────────────────────────────────────

/** Find an available TCP port. */
function findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      if (!addr || typeof addr === 'string') return reject(new Error('no address'));
      const port = addr.port;
      srv.close(() => resolve(port));
    });
  });
}

/** Poll until fn resolves or timeout. */
async function waitFor(fn: () => Promise<void>, timeoutMs = 5000, intervalMs = 100): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      await fn();
      return;
    } catch {
      await new Promise(r => setTimeout(r, intervalMs));
    }
  }
  await fn(); // final attempt — let it throw
}

interface ProxyInfo {
  port: number;
  servers: { name: string; port: number; pid: number; group: string }[];
}

async function getProxies(controlURL: string): Promise<ProxyInfo[]> {
  const resp = await fetch(`${controlURL}/__mdp/proxies`);
  return resp.json();
}

function serverNames(proxies: ProxyInfo[], port: number): string[] {
  const proxy = proxies.find(p => p.port === port);
  return proxy ? proxy.servers.map(s => s.name).sort() : [];
}

async function register(controlURL: string, opts: Record<string, unknown>): Promise<Response> {
  return fetch(`${controlURL}/__mdp/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(opts),
  });
}

async function heartbeat(controlURL: string, clientID: string): Promise<Response> {
  return fetch(`${controlURL}/__mdp/heartbeat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ clientID }),
  });
}

async function disconnect(controlURL: string, clientID: string): Promise<Response> {
  return fetch(`${controlURL}/__mdp/disconnect`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ clientID }),
  });
}

// ─── orchestrator lifecycle ─────────────────────────────────────────────────

let controlPort: number;
let controlURL: string;
let daemon: ChildProcess;

beforeAll(async () => {
  // Always rebuild to ensure tests run against current workspace changes.
  execSync('go build -o mdp ./cmd/mdp', { cwd: PROJECT_ROOT, stdio: 'pipe' });

  controlPort = await findFreePort();
  controlURL = `http://127.0.0.1:${controlPort}`;

  // Start orchestrator daemon in foreground (not -d, so we own the process)
  daemon = spawn(BINARY, ['--control-port', String(controlPort), '-d'], {
    cwd: PROJECT_ROOT,
    stdio: 'pipe',
    env: { ...process.env, _MDP_DAEMON: '1' },
  });

  // Wait for health endpoint
  await waitFor(async () => {
    const resp = await fetch(`${controlURL}/__mdp/health`);
    if (!resp.ok) throw new Error('not ready');
  });
}, 30_000);

afterAll(async () => {
  if (daemon && !daemon.killed) {
    // Graceful shutdown via API
    try {
      await fetch(`${controlURL}/__mdp/shutdown`, { method: 'POST' });
    } catch { /* ignore */ }
    // Give it a moment, then force kill
    await new Promise(r => setTimeout(r, 500));
    daemon.kill('SIGKILL');
  }
});

// ─── tests ──────────────────────────────────────────────────────────────────

describe('client sessions', () => {
  test('health endpoint is up', async () => {
    const resp = await fetch(`${controlURL}/__mdp/health`);
    expect(resp.ok).toBe(true);
    const body = await resp.json();
    expect(body.ok).toBe(true);
  });

  test('register with clientID', async () => {
    const clientID = 'test-register-' + Date.now();
    const resp = await register(controlURL, {
      name: 'sess/web',
      port: 59801,
      proxyPort: PROXY_PORT,
      group: 'sess',
      clientID,
    });
    expect(resp.ok).toBe(true);

    const proxies = await getProxies(controlURL);
    expect(serverNames(proxies, PROXY_PORT)).toContain('sess/web');

    // Cleanup
    await disconnect(controlURL, clientID);
  });

  test('heartbeat accepts valid clientID', async () => {
    const clientID = 'test-heartbeat-' + Date.now();

    // Register first to create the session
    await register(controlURL, {
      name: 'hb/svc',
      port: 59802,
      proxyPort: PROXY_PORT,
      group: 'hb',
      clientID,
    });

    const resp = await heartbeat(controlURL, clientID);
    expect(resp.ok).toBe(true);
    expect((await resp.json()).ok).toBe(true);

    await disconnect(controlURL, clientID);
  });

  test('heartbeat rejects empty clientID', async () => {
    const resp = await fetch(`${controlURL}/__mdp/heartbeat`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    });
    expect(resp.status).toBe(400);
  });

  test('disconnect removes all servers for a client', async () => {
    const clientID = 'test-disconnect-' + Date.now();

    // Register 3 servers with the same clientID
    for (const [name, port] of [['d/web', 59810], ['d/api', 59811], ['d/worker', 59812]] as const) {
      const resp = await register(controlURL, {
        name,
        port,
        proxyPort: PROXY_PORT,
        group: 'disc',
        clientID,
      });
      expect(resp.ok).toBe(true);
    }

    // Verify registered
    let proxies = await getProxies(controlURL);
    let names = serverNames(proxies, PROXY_PORT);
    expect(names).toContain('d/api');
    expect(names).toContain('d/web');
    expect(names).toContain('d/worker');

    // Disconnect — all 3 should be removed
    const resp = await disconnect(controlURL, clientID);
    expect(resp.ok).toBe(true);
    const body = await resp.json();
    expect(body.removed).toBe(3);

    // Verify gone
    proxies = await getProxies(controlURL);
    names = serverNames(proxies, PROXY_PORT);
    expect(names).not.toContain('d/api');
    expect(names).not.toContain('d/web');
    expect(names).not.toContain('d/worker');
  });

  test('disconnect does not affect other clients', async () => {
    const clientA = 'test-iso-a-' + Date.now();
    const clientB = 'test-iso-b-' + Date.now();

    await register(controlURL, {
      name: 'iso/a', port: 59820, proxyPort: PROXY_PORT, group: 'iso', clientID: clientA,
    });
    await register(controlURL, {
      name: 'iso/b', port: 59821, proxyPort: PROXY_PORT, group: 'iso', clientID: clientB,
    });

    // Disconnect A only
    await disconnect(controlURL, clientA);

    const proxies = await getProxies(controlURL);
    const names = serverNames(proxies, PROXY_PORT);
    expect(names).not.toContain('iso/a');
    expect(names).toContain('iso/b');

    await disconnect(controlURL, clientB);
  });

  test('disconnect rejects empty clientID', async () => {
    const resp = await fetch(`${controlURL}/__mdp/disconnect`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
    });
    expect(resp.status).toBe(400);
  });

  test('disconnect with unknown clientID returns removed=0', async () => {
    const resp = await disconnect(controlURL, 'nonexistent-client');
    expect(resp.ok).toBe(true);
    expect((await resp.json()).removed).toBe(0);
  });

  test('shutdown/watch blocks until shutdown', async () => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 500);

    try {
      await fetch(`${controlURL}/__mdp/shutdown/watch`, { signal: controller.signal });
      expect.unreachable('shutdown/watch should block, not return immediately');
    } catch (err: any) {
      expect(err.name).toBe('AbortError');
    } finally {
      clearTimeout(timeout);
    }
  });

  test('full lifecycle: register → heartbeat → disconnect', async () => {
    const clientID = 'test-lifecycle-' + Date.now();

    // Register
    const regResp = await register(controlURL, {
      name: 'life/svc',
      port: 59830,
      proxyPort: PROXY_PORT,
      group: 'life',
      clientID,
    });
    expect(regResp.ok).toBe(true);

    // Heartbeat
    const hbResp = await heartbeat(controlURL, clientID);
    expect(hbResp.ok).toBe(true);

    // Verify registered
    let proxies = await getProxies(controlURL);
    expect(serverNames(proxies, PROXY_PORT)).toContain('life/svc');

    // Disconnect
    const discResp = await disconnect(controlURL, clientID);
    expect(discResp.ok).toBe(true);
    expect((await discResp.json()).removed).toBe(1);

    // Verify gone
    proxies = await getProxies(controlURL);
    expect(serverNames(proxies, PROXY_PORT)).not.toContain('life/svc');
  });

  test('servers without clientID survive disconnect', async () => {
    const clientID = 'test-mixed-' + Date.now();

    // Register one with clientID, one without
    await register(controlURL, {
      name: 'mix/owned', port: 59840, proxyPort: PROXY_PORT, group: 'mix', clientID,
    });
    await register(controlURL, {
      name: 'mix/external', port: 59841, proxyPort: PROXY_PORT, group: 'mix',
    });

    // Disconnect client
    await disconnect(controlURL, clientID);

    // External server should survive
    const proxies = await getProxies(controlURL);
    const names = serverNames(proxies, PROXY_PORT);
    expect(names).not.toContain('mix/owned');
    expect(names).toContain('mix/external');

    // Manual cleanup
    await fetch(`${controlURL}/__mdp/register/mix/external`, { method: 'DELETE' });
  });
});
