// Host half of the AI Terminal plugin for DeepSeek Harness.
//
// It owns the lifecycle of the native Go terminal backend (cmd/terminal of this
// checkout), proxies its SPA and API under /ai-terminal/ on the Harness origin,
// and exposes a tiny status/restart control API for the Client panel. The
// Client half (client.js) renders the main panel and the sidebar entry.
//
// Security (plan Task 1 / R1): the Go backend only trusts loopback Hosts and
// same-origin unsafe requests, and it requires a per-launch token on /api/*.
// The plugin generates that token and the instance nonce (TERMINAL_API_TOKEN /
// TERMINAL_INSTANCE_ID), passes both to its child, injects the token into
// proxied requests, strips the Harness credentials, and adopts a backend on
// the port only when its /api/health reports the instance nonce of this launch
// (R10: do not trust whatever listens on the port).
//
// Configuration (bundle patch, row id "ai-terminal"):
//   mode:       "offline" | "live" | "auto"   (default "offline")
//   port:       local backend port            (default 18090)
//   autoStart:  start the backend on load     (default true)
//   repoDir:    checkout containing cmd/terminal (default: this package's parent)
//   binaryPath: prebuilt backend binary       (default: <repoDir>/bin/terminal)

import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { createAutonomy } from './autonomy.js';
import { createBackendBotTransport, createBotRelay } from './bot-relay.js';
import { createLLMBridge } from './llm-bridge.js';
import { createSkillProvider } from './skills.js';
import { createToolBridge } from './tools.js';

/**
 * The only hard requirement of the plugin row is the Harness Web server (the
 * panel and control routes). The tool registry is mounted through a child
 * ctx.inject(['tools']) below: a Cordis row whose required inject never appears
 * stays in the `loading` state, and the Desktop treats that as a fatal web
 * boot error ("1 entry did not activate"). Keeping the row's inject minimal
 * means a profile without tools/workflowEngine/schedule/... still boots; the
 * optional capabilities report "сервис недоступен" instead.
 */
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
  // Harness LLM used by the terminal agent through the plugin bridge (plan
  // Task 10): the user's provider/model, not a separate API key.
  llmProvider: 'deepseek-official',
  llmModel: 'deepseek-flash',
  // Optional directory of generated skill patterns (<dataDir>/skills, Task 19).
  dataDir: '',
  // Autonomous nightly research workflow (plan Task 21): disabled by default,
  // a token budget and a wall clock cancel the run; mutating proposals still
  // need human confirmation in the chat.
  autonomy: {
    enabled: false,
    tokenBudget: 200_000,
    maxWallMinutes: 60,
    approvalMode: 'confirm',
  },
  // Bot relay (plan Task 26): how many significant bot events per hour reach
  // the session agent directly; the excess is batched into one summary.
  relay: {
    maxAgentEventsPerHour: 12,
    summaryBatch: 5,
  },
};

// Environment the backend reads from the Harness process (ARCHITECTURE §2).
// The DSH subprocess service scrubs credential-shaped ambient variables before
// a child starts, so the terminal-specific ones are forwarded deliberately and
// nothing else of the Harness environment is: the backend needs no other
// Harness secret.
const BACKEND_ENV_NAMES = [
  'TRANSAQ_ADDR',
  'TRANSAQ_LOGIN',
  'TRANSAQ_PASSWORD',
  'TC_HOST',
  'TC_PORT',
  'TC_LOGIN',
  'TC_PASSWORD',
  'TERMINAL_LIVE_TRADING',
  'TERMINAL_LLM_BASE_URL',
  'TERMINAL_LLM_API_KEY',
  'TERMINAL_LLM_MODEL',
  'TERMINAL_LLM_TIMEOUT',
];

/**
 * Environment overrides for the backend child: the token, the nonce, the
 * plugin-owned LLM endpoint and the terminal env. explicit overrides win over
 * the ambient values (the plugin forwards the LLM base URL/token itself).
 */
function backendEnv(token, instanceId, overrides = {}) {
  const env = { TERMINAL_API_TOKEN: token, TERMINAL_INSTANCE_ID: instanceId };
  for (const name of BACKEND_ENV_NAMES) {
    const value = process.env[name];
    if (typeof value === 'string' && value !== '') env[name] = value;
  }
  for (const [name, value] of Object.entries(overrides)) {
    if (typeof value === 'string' && value !== '') env[name] = value;
  }
  return env;
}

/** Read and validate the row config, ignoring unknown keys. */
function normalizeConfig(config) {
  const cfg = { ...DEFAULTS, autonomy: { ...DEFAULTS.autonomy }, relay: { ...DEFAULTS.relay } };
  if (!config || typeof config !== 'object') return cfg;
  if (typeof config.mode === 'string' && ['offline', 'live', 'auto'].includes(config.mode)) cfg.mode = config.mode;
  if (Number.isInteger(config.port) && config.port >= 1024 && config.port <= 65535) cfg.port = config.port;
  if (typeof config.autoStart === 'boolean') cfg.autoStart = config.autoStart;
  if (typeof config.repoDir === 'string' && config.repoDir.trim() !== '') cfg.repoDir = path.resolve(config.repoDir.trim());
  if (typeof config.binaryPath === 'string' && config.binaryPath.trim() !== '') cfg.binaryPath = path.resolve(config.binaryPath.trim());
  if (typeof config.llmProvider === 'string' && config.llmProvider.trim() !== '') cfg.llmProvider = config.llmProvider.trim();
  if (typeof config.llmModel === 'string' && config.llmModel.trim() !== '') cfg.llmModel = config.llmModel.trim();
  if (typeof config.dataDir === 'string' && config.dataDir.trim() !== '') cfg.dataDir = path.resolve(config.dataDir.trim());
  if (config.autonomy && typeof config.autonomy === 'object') {
    if (typeof config.autonomy.enabled === 'boolean') cfg.autonomy.enabled = config.autonomy.enabled;
    if (Number.isInteger(config.autonomy.tokenBudget) && config.autonomy.tokenBudget >= 0) cfg.autonomy.tokenBudget = config.autonomy.tokenBudget;
    if (Number.isInteger(config.autonomy.maxWallMinutes) && config.autonomy.maxWallMinutes > 0) cfg.autonomy.maxWallMinutes = config.autonomy.maxWallMinutes;
    if (config.autonomy.approvalMode === 'confirm' || config.autonomy.approvalMode === 'auto') cfg.autonomy.approvalMode = config.autonomy.approvalMode;
  }
  if (config.relay && typeof config.relay === 'object') {
    if (Number.isInteger(config.relay.maxAgentEventsPerHour) && config.relay.maxAgentEventsPerHour >= 0) {
      cfg.relay.maxAgentEventsPerHour = config.relay.maxAgentEventsPerHour;
    }
    if (Number.isInteger(config.relay.summaryBatch) && config.relay.summaryBatch > 0) {
      cfg.relay.summaryBatch = config.relay.summaryBatch;
    }
  }
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
    // Per-launch secrets: the backend requires the token on /api/* and the
    // plugin accepts the port only when health reports this nonce.
    this.token = randomUUID();
    this.instanceId = randomUUID();
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
    const previous = this.state.phase;
    this.state = { ...this.state, phase, message, ...extra };
    if (phase === previous) return;
    // The plugin registers its Harness tools only while the backend is up;
    // every (re)start re-reads the Go tool catalog (plan Task 9).
    if (phase === 'running' && typeof this.onRunning === 'function') {
      try {
        this.onRunning();
      } catch {
        // Tool registration is best-effort: the terminal itself still works.
      }
    } else if (previous === 'running' && typeof this.onStopped === 'function') {
      try {
        this.onStopped();
      } catch {
        // Nothing to unregister if the registry is already gone.
      }
    }
  }

  /** Raw health probe: any process answering /api/health, without the nonce check. */
  async probe() {
    try {
      const { status, body } = await getJSON(this.cfg.port, '/api/health', 1_500);
      return status === 200 && body && body.ok === true ? body : null;
    } catch {
      return null;
    }
  }

  /** Health of our own backend: a foreign process on the port is not it (R10). */
  async health() {
    const body = await this.probe();
    return body && body.instanceId === this.instanceId ? body : null;
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
      // The backend reads the token and the instance nonce from the
      // environment; they are generated per plugin launch and never logged.
      env: backendEnv(this.token, this.instanceId, {
        ...this.envOverrides,
        // Use the same configured catalog as the host-side skill provider.
        TERMINAL_DATA_DIR: this.cfg.dataDir,
      }),
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
        // Something answers on the port but not with our instanceId: refuse to
        // adopt it instead of spawning over a process we do not own (R10).
        const foreign = await this.probe();
        if (foreign) {
          this.reused = false;
          this.set('error', 'порт занят чужим процессом: /api/health не подтвердил instanceId этого запуска');
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

/**
 * Proxy one request to the Go backend, preserving headers and streaming bodies.
 *
 * The Harness session credentials (cookie, authorization and x-dsh-* headers)
 * are never forwarded: the Go backend has no business seeing them. Instead the
 * per-launch terminal token is injected, which is the only credential the
 * backend accepts on /api/*. The original Host is kept so the backend can
 * verify that unsafe requests are same-origin (CSRF guard).
 */
function proxyToBackend(port, token, req, res) {
  if (typeof req.url !== 'string' || !req.url.startsWith(`${MOUNT}/`)) {
    json(res, 404, { error: { code: 'not_found', message: 'только /ai-terminal/…' } });
    return;
  }
  const rest = req.url.slice(MOUNT.length) || '/';
  const headers = { ...req.headers };
  delete headers.connection;
  delete headers['proxy-connection'];
  delete headers['keep-alive'];
  delete headers['transfer-encoding'];
  delete headers.upgrade;
  delete headers.cookie;
  delete headers.authorization;
  for (const name of Object.keys(headers)) {
    if (name.startsWith('x-dsh-')) delete headers[name];
  }
  headers['x-terminal-token'] = token;
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
    // The backend token cookie belongs to the backend origin; do not leak it
    // into the Harness cookie jar (the proxy authenticates with the header).
    delete responseHeaders['set-cookie'];
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

  // Harness tool bridge (plan Task 9): the Go agent catalog becomes native
  // terminal_<name> tools of the agents. The bridge is mounted through
  // ctx.inject(['tools']) at the end of apply(); without the tool service it
  // simply does not exist, and the plugin row still activates.
  let toolBridge = null;
  backend.onRunning = () => {
    if (!toolBridge) return;
    toolBridge.refresh().catch((error) => {
      console.warn('[ai-terminal] не удалось зарегистрировать инструменты Harness: '
        + (error instanceof Error ? error.message : String(error)));
    });
  };
  backend.onStopped = () => {
    if (toolBridge) toolBridge.unregister();
  };

  // --- Harness LLM bridge (plan Task 10) --------------------------------
  //
  // The terminal agent keeps its OpenAI-compatible client, but the provider is
  // the user's Harness LLM: the plugin registers the bridge under the control
  // path and hands the backend a one-time URL/token pair through its env.
  const llmToken = randomUUID();
  const llmService = typeof ctx.get === 'function' ? ctx.get('llm') : ctx.llm;
  const llmBridge = llmService && typeof llmService.stream === 'function'
    ? createLLMBridge({ llm: llmService, token: llmToken, provider: cfg.llmProvider, model: cfg.llmModel })
    : null;
  let dshOrigin = '';
  try {
    dshOrigin = typeof ctx.webServer.getOrigin === 'function' ? String(ctx.webServer.getOrigin()) : '';
  } catch {
    dshOrigin = '';
  }
  if (dshOrigin === '' && Number.isInteger(ctx.webServer.port)) dshOrigin = 'http://127.0.0.1:' + ctx.webServer.port;
  if (llmBridge) {
    own(ctx.webServer.register({
      kind: 'exact',
      path: CONTROL + '/llm/v1/chat/completions',
      handler: (req, res) => {
        Promise.resolve(llmBridge.handler(req, res)).catch((error) => {
          json(res, 500, { error: { message: 'внутренняя ошибка LLM-моста', type: 'internal_error', detail: error instanceof Error ? error.message : String(error) } });
        });
      },
    }));
    backend.envOverrides = {
      TERMINAL_LLM_BASE_URL: dshOrigin + CONTROL + '/llm/v1',
      TERMINAL_LLM_API_KEY: llmToken,
    };
  } else {
    backend.envOverrides = {};
  }

  // Skill provider: the repository skills/trading-* tree (same frontmatter as
  // "npx skills add") plus the generated patterns under <dataDir>/skills.
  const repoDir = findRepoDir(cfg.repoDir);
  const skillsService = typeof ctx.get === 'function' ? ctx.get('skills') : ctx.skills;
  if (skillsService && typeof skillsService.registerProvider === 'function') {
    const roots = [path.join(repoDir, 'skills')];
    if (cfg.dataDir !== '') roots.push(path.join(cfg.dataDir, 'skills'));
    own(ctx.effect(() => skillsService.registerProvider(() => createSkillProvider({ roots })), 'ai-terminal: skill provider'));
  }

  // --- Autonomous nightly research (plan Task 21) -----------------------
  //
  // autonomy.js owns the ai-terminal-nightly workflow run: the schedule is a
  // Harness task (00:30 Europe/Moscow, Tue-Sat) whose prompt calls the
  // terminal_nightly_research tool; the tool starts the workflow, the token
  // budget is enforced by intercepting ctx.llm streams, and the kill switch
  // cancels the run plus the backend autonomy runner. The script is read from
  // the plugin package's workflows/nightly-research.js as text: the workflow
  // engine executes the body in its own realm (the script is package data, not
  // a repository checkout file).
  const resolveService = (name) => (typeof ctx.get === 'function' ? ctx.get(name) : ctx[name]);
  let nightlyScript = '';
  try {
    nightlyScript = fs.readFileSync(path.join(pluginDir(), 'workflows', 'nightly-research.js'), 'utf8');
  } catch (error) {
    console.warn('[ai-terminal] скрипт ночного workflow недоступен: '
      + (error instanceof Error ? error.message : String(error)));
  }
  const autonomy = createAutonomy({
    ctx,
    cfg: cfg.autonomy,
    workflowEngine: resolveService('workflowEngine'),
    schedule: resolveService('schedule'),
    sessionController: resolveService('sessionController'),
    scriptText: nightlyScript,
    invokeTool: (name, args, signal) => (toolBridge
      ? toolBridge.invoke(name, args, signal)
      : Promise.reject(new Error('инструменты Harness недоступны'))),
    log: (level, message) => {
      const line = '[ai-terminal] ' + message;
      if (level === 'warn') console.warn(line);
      else console.log(line);
    },
  });
  own(() => autonomy.dispose());

  own(ctx.webServer.register({
    kind: 'exact',
    path: CONTROL + '/autonomy/status',
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
      json(res, 200, autonomy.status());
    },
  }));

  own(ctx.webServer.register({
    kind: 'exact',
    path: CONTROL + '/autonomy/run',
    handler: (req, res) => {
      if (req.method !== 'POST') {
        json(res, 405, { error: { code: 'method_not_allowed', message: 'только POST' } });
        return;
      }
      if (autonomy.status().running) {
        json(res, 409, { error: { code: 'already_running', message: 'ночное исследование уже выполняется' }, status: autonomy.status() });
        return;
      }
      // Start the run without blocking the control request: the panel polls
      // /autonomy/status, the workflow itself may run for minutes.
      void autonomy.runNow({ trigger: 'control' }).catch((error) => {
        console.warn('[ai-terminal] ночной workflow не запустился: '
          + (error instanceof Error ? error.message : String(error)));
      });
      json(res, 202, { started: true, status: autonomy.status() });
    },
  }));

  own(ctx.webServer.register({
    kind: 'exact',
    path: CONTROL + '/autonomy/stop',
    handler: async (req, res) => {
      if (req.method !== 'POST') {
        json(res, 405, { error: { code: 'method_not_allowed', message: 'только POST' } });
        return;
      }
      try {
        json(res, 200, await autonomy.stop());
      } catch (error) {
        json(res, 503, { error: { code: 'autonomy_stop_failed', message: error instanceof Error ? error.message : String(error) }, status: autonomy.status() });
      }
    },
  }));

  void autonomy.ensureSchedule().catch((error) => {
    console.warn('[ai-terminal] расписание ночного исследования не создано: '
      + (error instanceof Error ? error.message : String(error)));
  });

  // --- Bot control room relay (plan Task 26) ----------------------------
  //
  // The relay subscribes to the backend SSE "bot" events and injects only
  // significant ones into the DSH session that created the bot. The bot id to
  // session mapping comes from the terminal_bot_* tool calls: exec.agent.id is
  // the session id of the Harness agent registry (docs/ai-terminal/
  // dsh-integration.md §2.7). Without a live agent the relay falls back to
  // ctx.sessionController.prompt with the same content.
  const logLine = (level, message) => {
    const line = '[ai-terminal] ' + message;
    if (level === 'warn') console.warn(line);
    else console.log(line);
  };
  const botRelay = createBotRelay({
    agents: resolveService('agents'),
    sessionController: resolveService('sessionController'),
    transport: createBackendBotTransport({ port: cfg.port, token: backend.token, log: logLine }),
    log: logLine,
    maxEventsPerHour: cfg.relay.maxAgentEventsPerHour,
    summaryBatch: cfg.relay.summaryBatch,
  });
  own(() => botRelay.dispose());
  botRelay.start();

  // Mount the Harness tool integration only when the registry is available.
  // The plugin row itself does not require it (see the inject comment above):
  // the child fiber may wait without blocking the web boot.
  const mountTools = (toolCtx) => {
    const tools = toolCtx && typeof toolCtx.get === 'function'
      ? toolCtx.get('tools')
      : (toolCtx && toolCtx.tools);
    if (!tools || typeof tools.register !== 'function') return undefined;
    const bridge = createToolBridge({ tools, port: cfg.port, token: backend.token });
    toolBridge = bridge;
    const disposeNightly = autonomy.registerTool(tools);
    if (typeof bridge.setToolResultHook === 'function') {
      bridge.setToolResultHook((name, args, exec, reply) => botRelay.trackToolResult(name, args, exec, reply));
    }
    const release = () => {
      try { disposeNightly(); } catch { /* already unregistered */ }
      try { bridge.close(); } catch { /* already closed */ }
      if (toolBridge === bridge) toolBridge = null;
    };
    if (toolCtx && typeof toolCtx.effect === 'function') toolCtx.effect(() => release);
    if (backend.state.phase === 'running') backend.onRunning();
    return release;
  };
  if (typeof ctx.inject === 'function') {
    void ctx.inject(['tools'], mountTools);
  } else {
    mountTools(ctx);
  }

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
      proxyToBackend(cfg.port, backend.token, req, res);
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
    if (toolBridge) toolBridge.close();
    void backend.dispose();
  }, 'ai-terminal: routes, tools and backend');

  if (cfg.autoStart) {
    void backend.start();
  } else {
    backend.set('stopped', 'автозапуск выключен (autoStart: false)');
  }
}
