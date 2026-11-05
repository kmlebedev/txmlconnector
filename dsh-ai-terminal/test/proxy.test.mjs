// Tests for the DSH AI Terminal plugin host half (plan Task 1 / R10):
// - the proxy must strip the Harness credentials it forwards and inject the
//   per-launch terminal token instead;
// - a backend on the configured port is accepted only when /api/health reports
//   the instanceId nonce this plugin passed to its own child, so a foreign
//   process squatting on the port is never adopted.
//
//   node --test dsh-ai-terminal/test/

import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { test } from 'node:test';

const pluginFile = path.resolve(import.meta.dirname, '..', 'index.js');
const plugin = await import(pluginFile);

// --- helpers ---------------------------------------------------------------

/** Listen on an ephemeral loopback port and resolve it. */
function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(server.address().port));
  });
}

/** Close a server, dropping keep-alive connections first. */
function close(server) {
  return new Promise((resolve) => {
    if (typeof server.closeAllConnections === 'function') server.closeAllConnections();
    server.close(() => resolve());
  });
}

/** Reserve an ephemeral loopback port and release it for the backend. */
async function freePort() {
  const server = http.createServer();
  const port = await listen(server);
  await close(server);
  return port;
}

/** Poll check until it returns a truthy value or the timeout expires. */
async function waitFor(check, timeoutMs, label) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await check();
    if (value) return value;
    if (Date.now() > deadline) throw new Error('timeout: ' + label);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

// The backend stub: a real HTTP server that reports the instance nonce and the
// token it was started with, so the test can verify what the plugin passed.
const BACKEND_SOURCE = [
  '#!/usr/bin/env node',
  "import http from 'node:http';",
  '',
  'const arg = (name, fallback) => {',
  '  const index = process.argv.indexOf(name);',
  '  return index >= 0 ? process.argv[index + 1] : fallback;',
  '};',
  'const addr = arg("-addr", "127.0.0.1:0");',
  'const port = Number(addr.split(":").pop());',
  'const token = process.env.TERMINAL_API_TOKEN || "";',
  'const instanceId = process.env.TERMINAL_INSTANCE_ID || "";',
  '',
  'const server = http.createServer((req, res) => {',
  '  res.setHeader("content-type", "application/json");',
  '  if (req.url === "/api/health") {',
  '    res.end(JSON.stringify({ ok: true, mode: "offline", instanceId }));',
  '    return;',
  '  }',
  '  res.end(JSON.stringify({ path: req.url, headers: req.headers, token, instanceId }));',
  '});',
  '',
  'server.listen(port, "127.0.0.1");',
  'process.on("SIGTERM", () => process.exit(0));',
].join('\n');

/** Write the backend stub into a temporary directory and make it executable. */
function writeBackend(dir) {
  const file = path.join(dir, 'backend.mjs');
  fs.writeFileSync(file, BACKEND_SOURCE);
  fs.chmodSync(file, 0o755);
  return file;
}

/** Minimal stand-in for the Harness subprocess service (spawns real children). */
function fakeSubprocess() {
  const children = new Set();
  const spawns = [];
  return {
    spawns,
    async resolveExecutable(command) {
      return command;
    },
    spawn(spec) {
      spawns.push(spec);
      const child = spawn(spec.argv[0], spec.argv.slice(1), {
        cwd: spec.cwd,
        // The real service starts the child from a scrubbed parent env and
        // merges the explicit overrides; the test only needs them visible.
        env: { ...process.env, ...(spec.env || {}) },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      children.add(child);
      child.on('exit', () => children.delete(child));
      let stdout = '';
      let stderr = '';
      child.stdout.on('data', (chunk) => { stdout += chunk; });
      child.stderr.on('data', (chunk) => { stderr += chunk; });
      const done = new Promise((resolve) => child.on('exit', (code, signal) => resolve({ exitCode: code, signal })));
      const reader = (value) => ({ readFrom: () => ({ text: value, nextOffset: value.length, lossy: false }) });
      return {
        get collected() { return { stdout: reader(stdout), stderr: reader(stderr) }; },
        done,
        terminate() { child.kill('SIGTERM'); },
        waitForExit() { return done.then(() => true).catch(() => true); },
      };
    },
    killAll() {
      for (const child of children) child.kill('SIGKILL');
      children.clear();
    },
  };
}

/** Minimal stand-in for the Cordis context the plugin receives. */
function fakeCtx(subprocess) {
  const routes = [];
  const effects = [];
  return {
    routes,
    effects,
    webServer: {
      register(route) {
        routes.push(route);
        return () => {
          const index = routes.indexOf(route);
          if (index >= 0) routes.splice(index, 1);
        };
      },
    },
    get(name) {
      return name === 'subprocess' ? subprocess : undefined;
    },
    effect(fn) {
      const dispose = fn();
      if (typeof dispose === 'function') effects.push(dispose);
      return dispose;
    },
  };
}

/** Mount the plugin routes on a real HTTP server (the Harness webServer role). */
function mountRoutes(ctx) {
  return http.createServer((req, res) => {
    const pathname = (req.url || '/').split('?')[0];
    const exact = ctx.routes.find((route) => route.kind === 'exact' && route.path === pathname);
    if (exact) {
      Promise.resolve(exact.handler(req, res)).catch((error) => { res.statusCode = 500; res.end(String(error)); });
      return;
    }
    const prefixes = ctx.routes
      .filter((route) => route.kind === 'prefix' && pathname.startsWith(route.path))
      .sort((a, b) => b.path.length - a.path.length);
    if (prefixes[0]) {
      Promise.resolve(prefixes[0].handler(req, res)).catch((error) => { res.statusCode = 500; res.end(String(error)); });
      return;
    }
    res.statusCode = 404;
    res.end('no route');
  });
}

/** Create the temp dir, the ctx, the front server and apply the plugin. */
async function startPlugin(t, config, subprocess) {
  const ctx = fakeCtx(subprocess);
  const server = mountRoutes(ctx);
  const port = await listen(server);
  t.after(async () => {
    for (const dispose of ctx.effects.slice().reverse()) {
      try { await dispose(); } catch { /* already gone */ }
    }
    await close(server);
  });
  plugin.apply(ctx, config);
  return { ctx, server, port };
}

/** Read /ai-terminal-ctl/status of the mounted plugin. */
async function readStatus(port) {
  const response = await fetch('http://127.0.0.1:' + port + '/ai-terminal-ctl/status');
  return response.json();
}

// --- tests -----------------------------------------------------------------

test('proxy strips credentials and adds token', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ai-terminal-proxy-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const subprocess = fakeSubprocess();
  t.after(() => subprocess.killAll());
  const port = await freePort();
  const { server } = await startPlugin(t, {
    mode: 'offline',
    port,
    autoStart: true,
    binaryPath: writeBackend(dir),
  }, subprocess);
  const front = server.address().port;
  await waitFor(async () => (await readStatus(front)).phase === 'running', 20_000, 'backend start');

  const response = await fetch('http://127.0.0.1:' + front + '/ai-terminal/api/probe', {
    headers: {
      cookie: 'dsh_session=secret-cookie',
      authorization: 'Bearer secret-bearer',
      'x-dsh-session': 'secret-dsh-session',
      'x-dsh-control': 'secret-dsh-control',
      'x-terminal-token': 'poisoned-token',
      accept: 'application/json',
    },
  });
  assert.equal(response.status, 200, 'proxied request must reach the backend');
  const payload = await response.json();

  assert.equal(payload.headers.cookie, undefined, 'cookie header must not be forwarded');
  assert.equal(payload.headers.authorization, undefined, 'authorization header must not be forwarded');
  for (const name of Object.keys(payload.headers)) {
    assert.ok(!name.startsWith('x-dsh-'), 'x-dsh-* header leaked to the backend: ' + name);
  }
  assert.ok(payload.token, 'backend must receive TERMINAL_API_TOKEN in its env');
  assert.equal(
    payload.headers['x-terminal-token'],
    payload.token,
    'proxy must inject the backend token, not the browser-supplied value',
  );
  assert.notEqual(payload.headers['x-terminal-token'], 'poisoned-token');

  const state = await readStatus(front);
  assert.equal(state.phase, 'running');
  assert.equal(state.reused, false, 'a backend started by the plugin is not a reused foreign process');

  // The child gets the per-launch token and nonce, but not the whole Harness
  // environment (the subprocess service scrubs ambient secrets; the plugin
  // forwards only the terminal-specific variables it needs).
  const spec = subprocess.spawns[0];
  assert.ok(spec, 'the plugin must spawn its own backend');
  assert.ok(spec.env.TERMINAL_API_TOKEN, 'backend must receive TERMINAL_API_TOKEN');
  assert.ok(spec.env.TERMINAL_INSTANCE_ID, 'backend must receive TERMINAL_INSTANCE_ID');
  assert.equal(payload.token, spec.env.TERMINAL_API_TOKEN, 'the proxied token must be the one the backend was started with');
  assert.equal(spec.env.HOME, undefined, 'the plugin must not dump the whole Harness environment into the backend');
  assert.ok(!spec.env.PATH, 'the plugin must not forward the ambient PATH as an explicit override');
});

test('health rejects a foreign backend on the port', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ai-terminal-proxy-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const subprocess = fakeSubprocess();
  t.after(() => subprocess.killAll());
  const port = await freePort();
  const foreign = http.createServer((req, res) => {
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ ok: true, mode: 'offline', instanceId: 'foreign-instance' }));
  });
  t.after(() => close(foreign));
  await new Promise((resolve) => foreign.listen(port, '127.0.0.1', resolve));

  const { server } = await startPlugin(t, {
    mode: 'offline',
    port,
    autoStart: true,
    binaryPath: writeBackend(dir),
  }, subprocess);
  const front = server.address().port;

  const state = await waitFor(async () => {
    const current = await readStatus(front);
    return current.phase === 'error' ? current : null;
  }, 20_000, 'foreign backend detection');

  assert.match(state.message, /чуж/, 'error must explain that the port is taken by a foreign process');
  assert.equal(state.reused, false);
  assert.equal(subprocess.spawns.length, 0, 'a foreign backend must neither be adopted nor be overwritten');
});
