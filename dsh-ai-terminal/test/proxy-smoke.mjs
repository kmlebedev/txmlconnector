// Dev smoke test for the plugin Host half: mounts the registered routes on a
// real HTTP server, starts the Go backend and exercises the same-origin proxy
// (HTML, assets, REST GET/POST, SSE, restart) without the Harness UI.
//
//   node dsh-ai-terminal/test/proxy-smoke.mjs
//
// Requires the backend binary (`make terminal`) or a Go toolchain; uses ports
// 18097 (backend) and 18098 (test HTTP server), which must be free.
// Local smoke for the DSH AI Terminal plugin host half: mounts the registered
// routes on a real test HTTP server, starts the Go backend and exercises the
// same-origin proxy (HTML, JS, REST, SSE, restart) without the Harness UI.
import http from 'node:http';
import path from 'node:path';
import { spawn } from 'node:child_process';

const repo = path.resolve(import.meta.dirname, '..', '..');
const pluginFile = path.join(repo, 'dsh-ai-terminal', 'index.js');
const PORT = 18097;
const TEST_PORT = 18098;

const subprocess = {
  async resolveExecutable(command) { return command; },
  spawn(spec) {
    const child = spawn(spec.argv[0], spec.argv.slice(1), {
      cwd: spec.cwd,
      env: spec.env ?? process.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '', stderr = '';
    child.stdout.on('data', (d) => { stdout += d; });
    child.stderr.on('data', (d) => { stderr += d; });
    const done = new Promise((resolve) => child.on('exit', (code, signal) => resolve({ exitCode: code, signal })));
    const reader = (value) => ({ readFrom: () => ({ text: value, nextOffset: value.length, lossy: false }) });
    return {
      get collected() { return { stdout: reader(stdout), stderr: reader(stderr) }; },
      done,
      terminate() { child.kill('SIGTERM'); },
      waitForExit() { return done.then(() => true).catch(() => true); },
    };
  },
};

const routes = [];
const effects = [];
const ctx = {
  webServer: {
    register(route) {
      routes.push(route);
      return () => { const index = routes.indexOf(route); if (index >= 0) routes.splice(index, 1); };
    },
  },
  get(name) { return name === 'subprocess' ? subprocess : undefined; },
  effect(fn) { const dispose = fn(); if (typeof dispose === 'function') effects.push(dispose); return dispose; },
};

function match(pathname) {
  const exact = routes.find((route) => route.kind === 'exact' && route.path === pathname);
  if (exact) return exact;
  const prefixes = routes.filter((route) => route.kind === 'prefix' && pathname.startsWith(route.path))
    .sort((a, b) => b.path.length - a.path.length);
  return prefixes[0];
}

const server = http.createServer((req, res) => {
  const route = match((req.url || '/').split('?')[0]);
  if (!route) { res.statusCode = 404; res.end('no route'); return; }
  Promise.resolve(route.handler(req, res)).catch((error) => { res.statusCode = 500; res.end(String(error)); });
});
await new Promise((resolve) => server.listen(TEST_PORT, '127.0.0.1', resolve));

const plugin = await import(pluginFile);
const config = { mode: 'offline', port: PORT, autoStart: true };
plugin.apply(ctx, config);

const base = `http://127.0.0.1:${TEST_PORT}`;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function get(url, options) { const r = await fetch(url, options); const text = await r.text(); return { status: r.status, text, headers: r.headers }; }
async function waitStatus(timeoutMs = 60000) {
  const until = Date.now() + timeoutMs;
  while (Date.now() < until) {
    const r = await get(`${base}/ai-terminal-ctl/status`);
    const data = JSON.parse(r.text);
    if (data.phase === 'running') return data;
    await sleep(400);
  }
  throw new Error('backend did not start');
}

const report = {};
try {
  report.status = await waitStatus();
  report.index = await get(`${base}/ai-terminal/`);
  report.health = await get(`${base}/ai-terminal/api/health`);
  report.appjs = await get(`${base}/ai-terminal/app.js`);
  report.redirect = await get(`${base}/ai-terminal`, { redirect: 'manual' });
  report.orders = await get(`${base}/ai-terminal/api/orders`);
  const post = await fetch(`${base}/ai-terminal/api/orders`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ symbol: 'TQBR:SBER', side: 'buy', qty: 1, type: 'market' }),
  });
  report.order = { status: post.status, body: await post.json() };
  // SSE: read a couple of frames within 3 seconds.
  const controller = new AbortController();
  const sse = await fetch(`${base}/ai-terminal/api/events`, { signal: controller.signal });
  const reader = sse.body.getReader();
  const decoder = new TextDecoder();
  let frames = '';
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline && !frames.includes('\n\n')) {
    const chunk = await Promise.race([reader.read(), sleep(1000).then(() => ({ done: true }))]);
    if (chunk.done) break;
    frames += decoder.decode(chunk.value, { stream: true });
  }
  controller.abort();
  report.sse = { status: sse.status, contentType: sse.headers.get('content-type'), sample: frames.slice(0, 200) };
  report.restart = await get(`${base}/ai-terminal-ctl/restart`, { method: 'POST' });
  report.afterRestart = await waitStatus();
} finally {
  for (const dispose of effects.reverse()) {
    try { await dispose(); } catch {}
  }
  await sleep(1000);
  server.close();
}
console.log(JSON.stringify({
  status: { phase: report.status.phase, mode: report.status.mode, port: report.status.port },
  index: { status: report.index.status, hasTitle: report.index.text.includes('AI Terminal'), length: report.index.text.length },
  health: { status: report.health.status, mode: JSON.parse(report.health.text).mode },
  appjs: { status: report.appjs.status, contentType: report.appjs.headers.get('content-type'), length: report.appjs.text.length },
  redirect: { status: report.redirect.status, location: report.redirect.headers.get('location') },
  orders: { status: report.orders.status, body: report.orders.text.slice(0, 80) },
  order: { status: report.order.status, status2: report.order.body.order?.status, fill: report.order.body.order?.avgFillPrice },
  sse: report.sse,
  restart: { status: report.restart.status, body: report.restart.text.slice(0, 120) },
  afterRestart: { phase: report.afterRestart.phase, mode: report.afterRestart.mode },
}, null, 2));
