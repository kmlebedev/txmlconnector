// bot-relay.js — Host half of the bot control room (plan Task 26).
//
// Subscribes to the terminal backend SSE "bot" events, remembers which DSH
// session created each bot (the terminal_bot_* tool call carries exec.agent.id,
// which is the session id of the Harness agent registry; see
// docs/ai-terminal/dsh-integration.md §2.6/§2.7), and delivers only significant
// events to that session's agent through ctx.agents.inject(). closed_bar events
// without a signal go to the cards only: tokens must pay for decisions, not for
// every candle.
//
// Delivery policy: at most relay.maxEventsPerHour (default 12) per session per
// hour; excess events are buffered and flushed as one summary once the buffer
// reaches relay.summaryBatch (default 5). A reconnect after a backend restart
// resumes each bot journal from the last seen seq (GET /api/bots/{id}/journal
// ?after=seq), so no event is lost and none is delivered twice.
//
// Fallback: if no live agent is registered for the session, the relay uses
// ctx.sessionController.prompt with the same content and a [bot:<id>] marker
// (recorded as I in dsh-integration.md; ctx.agents.inject is the primary path).

import { eventComment, isSignificantEvent, parseBotFromReply } from './bot-ui.js';

const DEFAULT_MAX_EVENTS_PER_HOUR = 12;
const DEFAULT_SUMMARY_BATCH = 5;
const HOUR_MS = 3_600_000;
const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 10_000;
const JOURNAL_PAGE = 200;

/**
 * Parse one raw SSE frame ("event: ...\ndata: ...") into the decoded event.
 * Exported for tests of the transport framing.
 */
export function parseSseFrame(raw) {
  const lines = String(raw || '').split('\n');
  const data = [];
  let type = 'message';
  for (const line of lines) {
    if (line.startsWith('event:')) type = line.slice(6).trim();
    else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
  }
  if (data.length === 0) return null;
  let payload;
  try {
    payload = JSON.parse(data.join('\n'));
  } catch {
    return null;
  }
  if (payload === null || typeof payload !== 'object') return null;
  if (typeof payload.type === 'string') return payload;
  return { type, data: payload };
}

/**
 * Create the relay over one backend transport.
 *
 * options: { agents, sessionController, transport, maxEventsPerHour?,
 *            summaryBatch?, log?, now? }
 * The transport contract: subscribe({onEvent, onStatus}) -> stop, fetchBots(),
 * fetchJournal(botId, afterSeq, limit), stopAll().
 */
export function createBotRelay(options = {}) {
  const agents = options.agents;
  const sessionController = options.sessionController;
  const transport = options.transport;
  const log = typeof options.log === 'function' ? options.log : () => {};
  const now = typeof options.now === 'function' ? options.now : () => Date.now();
  const maxPerHour = Number.isInteger(options.maxEventsPerHour) && options.maxEventsPerHour >= 0
    ? options.maxEventsPerHour
    : DEFAULT_MAX_EVENTS_PER_HOUR;
  const summaryBatch = Number.isInteger(options.summaryBatch) && options.summaryBatch > 0
    ? options.summaryBatch
    : DEFAULT_SUMMARY_BATCH;

  const sessions = new Map(); // botId -> sessionId
  const bots = new Map(); // botId -> bot identity (for comments)
  const lastSeq = new Map(); // botId -> last observed journal seq
  const windows = new Map(); // sessionId -> { start, delivered, buffer[] }
  let stopTransport = null;
  let started = false;
  let openCount = 0;

  /** Remember the session that created the bot from one terminal_bot_* call. */
  function trackToolResult(toolName, args, exec, reply) {
    if (typeof toolName !== 'string' || !toolName.startsWith('bot_')) return;
    const bot = parseBotFromReply(reply);
    if (bot === null) return;
    bots.set(bot.id, bot);
    const agent = exec && exec.agent;
    const sessionId = agent && typeof agent.id === 'string' && agent.id !== ''
      ? agent.id
      : (agent && agent.session && typeof agent.session.id === 'string' ? agent.session.id : '');
    if (sessionId !== '') sessions.set(bot.id, sessionId);
  }

  function windowOf(sessionId) {
    let win = windows.get(sessionId);
    if (!win) {
      win = { start: now(), delivered: 0, buffer: [] };
      windows.set(sessionId, win);
    }
    if (now() - win.start >= HOUR_MS) {
      win.start = now();
      win.delivered = 0;
    }
    return win;
  }

  function inject(sessionId, text) {
    if (typeof sessionId !== 'string' || sessionId === '' || text === '') return;
    const handle = agents && typeof agents.get === 'function' ? agents.get(sessionId) : null;
    const agent = handle && handle.agent ? handle.agent : null;
    const content = [{ type: 'text', text }];
    if (agent && typeof agent.inject === 'function') {
      try {
        agent.inject({ content, source: { kind: 'ai-terminal-bot' } });
        return;
      } catch (error) {
        log('warn', 'ai-terminal: не удалось доставить событие бота агенту: ' + (error instanceof Error ? error.message : String(error)));
      }
    }
    if (sessionController && typeof sessionController.prompt === 'function') {
      try {
        void Promise.resolve(sessionController.prompt({
          sessionId,
          content,
          source: { kind: 'ai-terminal-bot' },
        })).catch((error) => {
          log('warn', 'ai-terminal: fallback prompt сессии не удался: ' + (error instanceof Error ? error.message : String(error)));
        });
        return;
      } catch (error) {
        log('warn', 'ai-terminal: fallback prompt сессии не удался: ' + (error instanceof Error ? error.message : String(error)));
      }
    }
    log('warn', 'ai-terminal: сессия ' + sessionId + ' не найдена, событие бота потеряно');
  }

  function flush(sessionId, win) {
    if (win.buffer.length === 0) return;
    const text = 'Сводка по ботам (' + win.buffer.length + ' событий за час): ' + win.buffer.join(' ');
    win.buffer = [];
    inject(sessionId, text);
  }

  function deliver(sessionId, text) {
    const win = windowOf(sessionId);
    if (win.buffer.length === 0 && win.delivered < maxPerHour) {
      win.delivered += 1;
      inject(sessionId, text);
      return;
    }
    win.buffer.push(text);
    if (win.buffer.length >= summaryBatch) flush(sessionId, win);
  }

  /** Observe one bot event ({botId, entry}); deduplicates by journal seq. */
  function observe(event) {
    if (!event || typeof event !== 'object') return;
    const data = event.type === 'bot' && event.data && typeof event.data === 'object' ? event.data : event;
    const botId = typeof data.botId === 'string' ? data.botId : '';
    const entry = data.entry && typeof data.entry === 'object' ? data.entry : null;
    if (botId === '' || entry === null) return;
    const seq = Number.isFinite(entry.seq) ? entry.seq : 0;
    if (seq > 0 && (lastSeq.get(botId) || 0) >= seq) return;
    if (seq > 0) lastSeq.set(botId, seq);
    if (!isSignificantEvent(entry)) return;
    const sessionId = sessions.get(botId) || '';
    if (sessionId === '') return;
    deliver(sessionId, eventComment(bots.get(botId) || { id: botId }, entry));
  }

  /** Replay missed journal entries after a reconnect, from the last seen seq. */
  async function resubscribe() {
    if (!transport || typeof transport.fetchBots !== 'function') return;
    let list = [];
    try {
      list = await transport.fetchBots();
    } catch (error) {
      log('warn', 'ai-terminal: список ботов недоступен при реконнекте: ' + (error instanceof Error ? error.message : String(error)));
      return;
    }
    for (const bot of Array.isArray(list) ? list : []) {
      if (!bot || typeof bot.id !== 'string' || bot.id === '') continue;
      const after = lastSeq.get(bot.id) || 0;
      let entries = [];
      try {
        entries = await transport.fetchJournal(bot.id, after, JOURNAL_PAGE);
      } catch (error) {
        log('warn', 'ai-terminal: журнал бота ' + bot.id + ' недоступен: ' + (error instanceof Error ? error.message : String(error)));
        continue;
      }
      for (const entry of Array.isArray(entries) ? entries : []) observe({ botId: bot.id, entry });
    }
  }

  function start() {
    if (started || !transport || typeof transport.subscribe !== 'function') return;
    started = true;
    stopTransport = transport.subscribe({
      onEvent: (event) => observe(event),
      onStatus: (status) => {
        if (status === 'open') {
          // The first open is the initial connection: live events only. A later
          // open is a reconnect after a restart: replay the missed journal.
          if (openCount > 0) void resubscribe();
          openCount += 1;
        }
      },
    });
  }

  async function stopAll() {
    if (!transport || typeof transport.stopAll !== 'function') return null;
    return transport.stopAll();
  }

  function dispose() {
    if (typeof stopTransport === 'function') {
      try {
        stopTransport();
      } catch {
        // the transport already closed
      }
    }
    stopTransport = null;
    started = false;
  }

  return {
    start,
    dispose,
    stopAll,
    observe,
    resubscribe,
    trackToolResult,
    sessions: () => new Map(sessions),
    lastSeq: () => new Map(lastSeq),
  };
}

/** One JSON request to the local terminal backend. */
async function backendJSON(baseURL, headers, pathname, init = {}) {
  const response = await fetch(baseURL + pathname, { ...init, headers: { ...headers, ...(init.headers || {}) } });
  if (!response.ok) throw new Error('HTTP ' + response.status + ' ' + pathname);
  const text = await response.text();
  if (text === '') return null;
  return JSON.parse(text);
}

/**
 * HTTP transport over the local terminal backend: SSE /api/events plus the
 * bots REST endpoints. Reconnects with a bounded backoff; every reconnect
 * reports "open" so the relay replays the missed journals.
 */
export function createBackendBotTransport(options = {}) {
  const port = options.port;
  const token = options.token;
  const log = typeof options.log === 'function' ? options.log : () => {};
  const fetchImpl = options.fetchImpl || globalThis.fetch;
  const baseURL = 'http://127.0.0.1:' + port;
  const headers = { 'x-terminal-token': token, accept: 'application/json' };

  function subscribe(handlers) {
    const controller = new AbortController();
    let closed = false;
    const run = async () => {
      let delay = RECONNECT_MIN_MS;
      while (!closed) {
        try {
          const response = await fetchImpl(baseURL + '/api/events', {
            headers: { ...headers, accept: 'text/event-stream' },
            signal: controller.signal,
          });
          if (!response.ok) throw new Error('SSE HTTP ' + response.status);
          if (handlers.onStatus) handlers.onStatus('open');
          delay = RECONNECT_MIN_MS;
          let buffer = '';
          const decoder = new TextDecoder();
          for await (const chunk of response.body) {
            if (closed) break;
            buffer += typeof chunk === 'string' ? chunk : decoder.decode(chunk, { stream: true });
            let boundary = buffer.indexOf('\n\n');
            while (boundary >= 0) {
              const frame = buffer.slice(0, boundary);
              buffer = buffer.slice(boundary + 2);
              const event = parseSseFrame(frame);
              if (event && handlers.onEvent) handlers.onEvent(event);
              boundary = buffer.indexOf('\n\n');
            }
          }
        } catch (error) {
          if (closed || controller.signal.aborted) return;
          log('warn', 'ai-terminal: SSE ботов оборвался, переподключение: ' + (error instanceof Error ? error.message : String(error)));
        }
        if (closed) return;
        if (handlers.onStatus) handlers.onStatus('closed');
        await new Promise((resolve) => {
          const timer = setTimeout(resolve, delay);
          if (timer && typeof timer.unref === 'function') timer.unref();
        });
        delay = Math.min(delay * 2, RECONNECT_MAX_MS);
      }
    };
    void run();
    return () => {
      closed = true;
      controller.abort();
    };
  }

  return {
    subscribe,
    async fetchBots() {
      const data = await backendJSON(baseURL, headers, '/api/bots', {});
      return data && Array.isArray(data.bots) ? data.bots : [];
    },
    async fetchJournal(botId, afterSeq, limit) {
      const data = await backendJSON(
        baseURL,
        headers,
        '/api/bots/' + encodeURIComponent(botId) + '/journal?after=' + String(afterSeq || 0) + '&limit=' + String(limit || 200),
        {},
      );
      return data && Array.isArray(data.entries) ? data.entries : [];
    },
    async stopAll() {
      return backendJSON(baseURL, headers, '/api/bots/stop-all', { method: 'POST' });
    },
  };
}

/**
 * Plan Task 26 interface: start the relay over a backend and return its stop
 * function. The relay object itself is available as returned.stop.
 */
export function startBotRelay(ctx, backend, options = {}) {
  const get = (name) => (ctx && typeof ctx.get === 'function' ? ctx.get(name) : ctx ? ctx[name] : undefined);
  const transport = options.transport || createBackendBotTransport({ port: backend.port, token: backend.token });
  const relay = createBotRelay({
    agents: get('agents'),
    sessionController: get('sessionController'),
    transport,
    log: options.log,
    maxEventsPerHour: options.maxEventsPerHour,
    summaryBatch: options.summaryBatch,
  });
  relay.start();
  const stop = () => relay.dispose();
  stop.relay = relay;
  return stop;
}
