// Host half of the AI Terminal plugin for DeepSeek Harness.
//
// It owns the lifecycle of the native Go terminal backend (cmd/terminal of this
// checkout), proxies its SPA and API under /ai-terminal/ on the Harness origin,
// and exposes a tiny status/restart control API for the Client panel. The
// Client half (client.js) renders the main panel and the sidebar entry.
//
// Configuration (bundle patch, row id "ai-terminal"):
//   mode:       "offline" | "live" | "auto"   (default "offline")
//   port:       local backend port            (default 18090)
//   autoStart:  start the backend on load     (default true)
//   repoDir:    checkout containing cmd/terminal (default: this package's parent)
//   binaryPath: prebuilt backend binary       (default: <repoDir>/bin/terminal)

import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** Services required by the plugin; without them the row stays inactive. */
export const inject = ['webServer'];

const MOUNT = '/ai-terminal';
const CONTROL = '/ai-terminal-ctl';
const DEFAULT_PORT = 18090;
const HEALTH_TIMEOUT_MS = 90_000;
const HEALTH_POLL_MS = 400;
const STOP_TIMEOUT_MS = 5_000;

const DEFAULTS = {
  mode: 'offline',
  port: DEFAULT_PORT,
  autoStart: true,
  repoDir: '',
  binaryPath: '',
};

/** Read and validate the row config, ignoring unknown keys. */
function normalizeConfig(config) {
  const cfg = { ...DEFAULTS };
  if (!config || typeof config !== 'object') return cfg;
  if (typeof config.mode === 'string' && ['offline', 'live', 'auto'].includes(config.mode)) cfg.mode = config.mode;
  if (Number.isInteger(config.port) && config.port >= 1024 && config.port <= 65535) cfg.port = config.port;
  if (typeof config.autoStart === 'boolean') cfg.autoStart = config.autoStart;
  if (typeof config.repoDir === 'string' && config.repoDir.trim() !== '') cfg.repoDir = path.resolve(config.repoDir.trim());
  if (typeof config.binaryPath === 'string' && config.binaryPath.trim() !== '') cfg.binaryPath = path.resolve(config.binaryPath.trim());
  return cfg;
}

/** Absolute package directory, following the profile symlink to the checkout. */
function pluginDir() {
  try {
    return path.dirname(fs.realpathSync(fileURLToPath(import.meta.url)));
  } catch {
    return path.dirname(fileURLToPath(import.meta.url));
  }
}

/** Locate the checkout that contains cmd/terminal, walking up from the plugin. */
function findRepoDir(configured) {
  const hasTerminal = (dir) => fs.existsSync(path.join(dir, 'cmd', 'terminal', 'main.go'));
  if (configured) return configured;
  let dir = pluginDir();
  for (let depth = 0; depth < 6; depth += 1) {
    if (hasTerminal(dir)) return dir;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return path.dirname(pluginDir());
}

function json(res, status, body) {
  const data = JSON.stringify(body);
  res.statusCode = status;
  res.setHeader('content-type', 'application/json; charset=utf-8');
  res.setHeader('cache-control', 'no-store');
  res.setHeader('content-length', Buffer.byteLength(data));
  res.end(data);
}

/** Minimal HTTP GET returning parsed JSON, used for backend health checks. */
function getJSON(port, pathname, timeoutMs = 2_000) {
  return new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port, path: pathname, method: 'GET', timeout: timeoutMs },
      (res) => {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (chunk) => { body += chunk; });
        res.on('end', () => {
          try {
            resolve({ status: res.statusCode, body: JSON.parse(body) });
          } catch (error) {
            reject(error);
          }
        });
      });
    req.on('timeout', () => req.destroy(new Error('timeout')));
    req.on('error', reject);
    req.end();
  });
}

/** The backend process manager shared by the control routes and the panel. */
class TerminalBackend {
  constructor(ctx, cfg) {
    this.ctx = ctx;
    this.cfg = cfg;
    this.child = null;
    this.reused = false;
    this.starting = null;
    this.state = {
      phase: 'stopped',
      message: 'бэкенд не запущен',
      mode: cfg.mode,
      port: cfg.port,
      reused: false,
      updatedAt: new Date().toISOString(),
    };
  }

  snapshot() {
    return { ...this.state, updatedAt: new Date().toISOString() };
  }

  set(phase, message, extra = {}) {
    this.state = { ...this.state, phase, message, ...extra };
  }

  async health() {
    try {
      const { status, body } = await getJSON(this.cfg.port, '/api/health', 1_500);
      return status === 200 && body && body.ok === true ? body : null;
    } catch {
      return null;
    }
  }

  async resolveBinary() {
    if (this.cfg.binaryPath) {
      if (!fs.existsSync(this.cfg.binaryPath)) throw new Error(`binaryPath не найден: ${this.cfg.binaryPath}`);
      return this.cfg.binaryPath;
    }
    const repo = findRepoDir(this.cfg.repoDir);
    const binary = path.join(repo, 'bin', 'terminal');
    if (fs.existsSync(binary)) return binary;
    const subprocess = this.ctx.get('subprocess');
    if (!subprocess) throw new Error('сервис subprocess недоступен: соберите терминал вручную (make terminal)');
    const go = await subprocess.resolveExecutable('go');
    this.set('starting', 'сборка Go-бэкенда…');
    fs.mkdirSync(path.dirname(binary), { recursive: true });
    const build = subprocess.spawn({
      argv: [go, 'build', '-o', binary, './cmd/terminal'],
      cwd: repo,
      stdio: { stdin: 'ignore', stdout: { maxBytes: 65_536 }, stderr: { maxBytes: 65_536 } },
      graceMs: 5_000,
    });
    const outcome = await build.done;
    if (outcome.exitCode !== 0) {
      const err = build.collected.stderr ? build.collected.stderr.readFrom(0).text.trim() : '';
      throw new Error(`сборка завершилась с кодом ${outcome.exitCode}${err ? `: ${err}` : ''}`);
    }
    return binary;
  }

  async startFresh() {
    const subprocess = this.ctx.get('subprocess');
    if (!subprocess) throw new Error('сервис subprocess недоступен');
    const binary = await this.resolveBinary();
    const repo = findRepoDir(this.cfg.repoDir);
    this.set('starting', 'запуск Go-бэкенда…');
    const handle = subprocess.spawn({
      argv: [binary, '-mode', this.cfg.mode, '-addr', `127.0.0.1:${this.cfg.port}`],
      cwd: repo,
      stdio: {
        stdin: 'ignore',
        stdout: { maxBytes: 1_048_576 },
        stderr: { maxBytes: 1_048_576 },
      },
      graceMs: STOP_TIMEOUT_MS,
    });
    this.child = handle;
    this.reused = false;
    handle.done.then((outcome) => {
      if (this.child !== handle) return;
      this.child = null;
      const err = handle.collected.stderr ? handle.collected.stderr.readFrom(0).text.trim() : '';
      this.set('error', `бэкенд остановился (код ${outcome.exitCode}${outcome.signal ? `, ${outcome.signal}` : ''})${err ? `: ${err}` : ''}`);
    }).catch((error) => {
      if (this.child === handle) this.child = null;
      this.set('error', `бэкенд упал: ${error instanceof Error ? error.message : String(error)}`);
    });
    const deadline = Date.now() + HEALTH_TIMEOUT_MS;
    for (;;) {
      const health = await this.health();
      if (health) {
        this.set('running', health.mode === 'live' ? 'живой рынок (read-only)' : 'офлайн-снапшот', {
          mode: health.mode || this.cfg.mode,
          reused: false,
        });
        return;
      }
      if (this.child === null) {
        // The done handler already described the failure.
        throw new Error(this.state.message);
      }
      if (Date.now() > deadline) {
        await this.stop();
        throw new Error(`бэкенд не ответил за ${Math.round(HEALTH_TIMEOUT_MS / 1000)} с`);
      }
      await new Promise((resolve) => setTimeout(resolve, HEALTH_POLL_MS));
    }
  }

  async start() {
    if (this.starting) return this.starting;
    this.starting = (async () => {
      try {
        const health = await this.health();
        if (health) {
          this.reused = true;
          this.set('running', 'подключено к уже запущенному бэкенду', {
            mode: health.mode || this.cfg.mode,
            reused: true,
          });
          return;
        }
        await this.startFresh();
      } catch (error) {
        this.set('error', error instanceof Error ? error.message : String(error));
      } finally {
        this.starting = null;
      }
    })();
    return this.starting;
  }

  async stop() {
    const child = this.child;
    this.child = null;
    this.reused = false;
    if (!child) return;
    try {
      child.terminate();
    } catch {
      // Already gone.
    }
    let timer;
    const timeout = new Promise((resolve) => { timer = setTimeout(() => resolve(false), STOP_TIMEOUT_MS); });
    try {
      await Promise.race([child.waitForExit().catch(() => true), timeout]);
    } finally {
      clearTimeout(timer);
    }
  }

  async restart() {
    if (this.reused) throw new Error('бэкенд запущен вне плагина — перезапустите его вручную');
    await this.stop();
    await this.start();
    if (this.state.phase !== 'running') throw new Error(this.state.message);
  }

  dispose() {
    return this.stop();
  }
}

/** Proxy one request to the Go backend, preserving headers and streaming bodies. */
function proxyToBackend(port, req, res) {
  if (typeof req.url !== 'string' || !req.url.startsWith(`${MOUNT}/`)) {
    json(res, 404, { error: { code: 'not_found', message: 'только /ai-terminal/…' } });
    return;
  }
  const rest = req.url.slice(MOUNT.length) || '/';
  const headers = { ...req.headers, host: `127.0.0.1:${port}` };
  delete headers.connection;
  delete headers['proxy-connection'];
  delete headers['keep-alive'];
  delete headers['transfer-encoding'];
  delete headers.upgrade;
  const upstream = http.request({
    host: '127.0.0.1',
    port,
    path: rest,
    method: req.method,
    headers,
  }, (upstreamRes) => {
    const responseHeaders = { ...upstreamRes.headers };
    delete responseHeaders.connection;
    delete responseHeaders['keep-alive'];
    delete responseHeaders['transfer-encoding'];
    delete responseHeaders.upgrade;
    res.writeHead(upstreamRes.statusCode || 502, responseHeaders);
    upstreamRes.pipe(res);
    upstreamRes.on('error', () => res.destroy());
  });
  upstream.on('error', (error) => {
    if (!res.headersSent) {
      json(res, 502, { error: { code: 'backend_unavailable', message: `бэкенд недоступен: ${error.message}` } });
    } else {
      res.destroy();
    }
  });
  req.on('aborted', () => upstream.destroy());
  res.on('close', () => upstream.destroy());
  if (req.method === 'GET' || req.method === 'HEAD') {
    upstream.end();
  } else {
    req.pipe(upstream);
  }
}

/** Plugin apply: manager lifecycle, mount routes and the status/restart API. */
export function apply(ctx, config) {
  const cfg = normalizeConfig(config);
  const backend = new TerminalBackend(ctx, cfg);
  const disposers = [];
  const own = (disposer) => {
    disposers.push(disposer);
    return disposer;
  };

  own(ctx.webServer.register({
    kind: 'exact',
    path: MOUNT,
    handler: (_req, res) => {
      res.statusCode = 302;
      res.setHeader('location', `${MOUNT}/`);
      res.end();
    },
  }));

  own(ctx.webServer.register({
    kind: 'prefix',
    path: MOUNT,
    handler: (req, res) => {
      if (backend.state.phase !== 'running' && !(backend.starting || backend.child)) {
        // The panel polls the status route; answer API calls with a clear 503
        // instead of an opaque 502 while the backend is not up yet.
        json(res, 503, { error: { code: 'backend_unavailable', message: backend.state.message } });
        return;
      }
      proxyToBackend(cfg.port, req, res);
    },
  }));

  own(ctx.webServer.register({
    kind: 'exact',
    path: `${CONTROL}/status`,
    handler: (req, res) => {
      if (req.method !== 'GET' && req.method !== 'HEAD') {
        json(res, 405, { error: { code: 'method_not_allowed', message: 'только GET' } });
        return;
      }
      if (req.method === 'HEAD') {
        res.statusCode = 200;
        res.end();
        return;
      }
      json(res, 200, { ...backend.snapshot(), url: `${MOUNT}/`, config: { mode: cfg.mode, port: cfg.port, autoStart: cfg.autoStart } });
    },
  }));

  own(ctx.webServer.register({
    kind: 'exact',
    path: `${CONTROL}/restart`,
    handler: async (req, res) => {
      if (req.method !== 'POST') {
        json(res, 405, { error: { code: 'method_not_allowed', message: 'только POST' } });
        return;
      }
      try {
        await backend.restart();
        json(res, 200, backend.snapshot());
      } catch (error) {
        json(res, 503, { error: { code: 'restart_failed', message: error instanceof Error ? error.message : String(error) }, status: backend.snapshot() });
      }
    },
  }));

  ctx.effect(() => () => {
    for (const dispose of disposers.reverse()) {
      try { dispose(); } catch { /* registration already gone */ }
    }
    void backend.dispose();
  }, 'ai-terminal: routes and backend');

  if (cfg.autoStart) {
    void backend.start();
  } else {
    backend.set('stopped', 'автозапуск выключен (autoStart: false)');
  }
}
