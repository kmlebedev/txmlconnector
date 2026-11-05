// api.js — REST + SSE client for the AI Terminal backend, plus a tiny observable
// store shared by every UI panel. `?mock=1` lazily loads terminal/web/mock/mock.js
// and never touches the network apart from the static fixture files.

export class ApiError extends Error {
  constructor(code, message, status = 0) {
    super(message || code || 'request failed');
    this.name = 'ApiError';
    this.code = code || 'internal';
    this.status = status;
  }
}

export class Store {
  constructor(initial = {}) {
    this.state = initial;
    this.listeners = new Set();
  }

  get() {
    return this.state;
  }

  /** Shallow-merge patch, notify every subscriber. */
  set(patch, source = 'ui') {
    if (!patch || typeof patch !== 'object') return;
    this.state = Object.assign(this.state, patch);
    this.emit('change', { patch, source });
  }

  /** Deep-ish patch for nested groups (ui, agent, ...). */
  patchGroup(group, patch, source = 'ui') {
    this.state[group] = Object.assign({}, this.state[group], patch);
    this.emit('change', { patch: { [group]: patch }, source });
  }

  emit(type, data) {
    for (const listener of this.listeners) {
      try {
        listener(type, data, this.state);
      } catch (error) {
        console.error('[store] listener failed', error);
      }
    }
  }

  on(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

/** Returns a fresh store state. UI-only groups start with sensible defaults. */
export function createInitialState() {
  return {
    ready: false,
    loading: true,
    error: null,
    health: null,
    mode: 'offline',
    connected: false,
    serverTime: new Date().toISOString(),
    exchangeTZ: 'Europe/Moscow',
    instruments: [],
    selected: null,
    periodSec: 300,
    candles: [],
    historyMeta: null,
    candlesLoading: false,
    orderbook: null,
    orderbookLoading: false,
    trades: [],
    hints: null,
    portfolio: null,
    orders: [],
    tradeHistory: [],
    backtests: [],
    strategies: [],
    agentTools: [],
    agentStatus: null,
    autonomy: null,
    autonomyLog: [],
    chat: [],
    chatBusy: false,
    llm: null,
    overlay: null,
    ui: {
      page: 'favorites',
      watchFilter: '',
      rightTab: 'orderbook',
      bottomTab: 'portfolio',
      bottomCollapsed: false,
      ticketSide: 'buy',
      ticketType: 'market',
      ticketQty: 1,
      ticketPrice: '',
      ticketSymbol: null,
      drawerOpen: false,
      drawerTab: 'chat',
      searchOpen: false,
      orderbookClickFlash: null,
    },
  };
}

const JSON_HEADERS = { 'Content-Type': 'application/json', Accept: 'application/json' };

// The SPA can be served standalone at the origin root (Go backend) or mounted
// under a path by an embedding host (the DSH plugin proxies it at
// /ai-terminal/). Calls below are absolute (/api/...) for the standalone
// server, so the mount prefix is prepended when the page is not at the root.
export function apiBaseFromLocation(location = globalThis.location) {
  const override = globalThis.__AI_TERMINAL_BASE__;
  if (typeof override === 'string') return override;
  const path = location && typeof location.pathname === 'string' ? location.pathname : '/';
  if (path === '/ai-terminal' || path.startsWith('/ai-terminal/')) return '/ai-terminal';
  return '';
}

export class ApiClient {
  constructor({ base = '', store = null } = {}) {
    this.base = base;
    this.store = store;
    this.isMock = false;
    this.es = null;
    this.listeners = new Set();
    this.reconnectAttempt = 0;
    this.reconnectTimer = 0;
    this.closed = false;
    this.sseState = 'idle';
  }

  // ---- transport -------------------------------------------------------

  async request(path, { method = 'GET', body = null, timeoutMs = 20000 } = {}) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const response = await fetch(`${this.base}${path}`, {
        method,
        headers: body ? JSON_HEADERS : { Accept: 'application/json' },
        body: body ? JSON.stringify(body) : undefined,
        signal: controller.signal,
      });
      const text = await response.text();
      let data = null;
      if (text) {
        try {
          data = JSON.parse(text);
        } catch {
          data = null;
        }
      }
      if (!response.ok) {
        const err = data && data.error ? data.error : {};
        throw new ApiError(err.code || `http_${response.status}`,
          err.message || `HTTP ${response.status}`, response.status);
      }
      return data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      if (error && error.name === 'AbortError') throw new ApiError('timeout', 'Превышено время ожидания ответа сервера');
      throw new ApiError('network', 'Нет соединения с сервером');
    } finally {
      clearTimeout(timer);
    }
  }

  // ---- REST endpoints (§10.1) -----------------------------------------

  async health() {
    return (await this.request('/api/health', { timeoutMs: 8000 })) || {};
  }

  async instruments() {
    const data = await this.request('/api/instruments');
    return { serverTime: data.serverTime, instruments: data.instruments || [] };
  }

  async candles(symbol, periodSec, count = 400) {
    const data = await this.request(
      `/api/instruments/${encodeURIComponent(symbol)}/candles?period=${periodSec}&count=${count}`);
    return {
      symbol: data.symbol || symbol,
      periodSec: data.periodSec || periodSec,
      source: data.source || '',
      note: data.note || '',
      candles: data.candles || [],
    };
  }

  async orderbook(symbol) {
    return await this.request(`/api/instruments/${encodeURIComponent(symbol)}/orderbook`);
  }

  async trades(symbol, limit = 60) {
    const data = await this.request(
      `/api/instruments/${encodeURIComponent(symbol)}/trades?limit=${limit}`);
    return data.trades || [];
  }

  async hints(symbol, periodSec) {
    return await this.request(`/api/instruments/${encodeURIComponent(symbol)}/hints?period=${periodSec}`);
  }

  async portfolio() {
    return await this.request('/api/portfolio');
  }

  async orders() {
    const data = await this.request('/api/orders');
    return data.orders || [];
  }

  async tradeHistory() {
    const data = await this.request('/api/trades');
    return data.trades || [];
  }

  async placeOrder(payload) {
    const data = await this.request('/api/orders', { method: 'POST', body: payload });
    return (data && data.order) || data;
  }

  async cancelOrder(id) {
    return await this.request(`/api/orders/${encodeURIComponent(id)}`, { method: 'DELETE' });
  }

  async strategies() {
    const data = await this.request('/api/backtest/strategies');
    return data.strategies || [];
  }

  async backtest(payload) {
    const data = await this.request('/api/backtest', { method: 'POST', body: payload, timeoutMs: 60000 });
    return (data && data.result) || data;
  }

  async agentTools() {
    const data = await this.request('/api/agent/tools');
    return data.tools || [];
  }

  async agentChat(payload) {
    return await this.request('/api/agent/chat', { method: 'POST', body: payload, timeoutMs: 120000 });
  }

  async agentStatus() {
    return await this.request('/api/agent/status');
  }

  async autonomy(payload) {
    const data = await this.request('/api/agent/autonomy', { method: 'POST', body: payload, timeoutMs: 30000 });
    return (data && data.autonomy) || data;
  }

  async autonomyLog(limit = 200) {
    const data = await this.request(`/api/agent/autonomy/log?limit=${limit}`);
    return data.log || [];
  }

  async agentBacktests() {
    const data = await this.request('/api/agent/backtests');
    return data.results || [];
  }

  // ---- SSE -------------------------------------------------------------

  onEvent(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  emitEvent(message) {
    for (const listener of this.listeners) {
      try {
        listener(message);
      } catch (error) {
        console.error('[sse] listener failed', error);
      }
    }
  }

  setSseState(state) {
    if (this.sseState === state) return;
    this.sseState = state;
    this.emitEvent({ type: '_sse', data: { state } });
  }

  connectEvents() {
    this.closed = false;
    this.openEventSource();
  }

  openEventSource() {
    if (this.closed || typeof EventSource === 'undefined') return;
    clearTimeout(this.reconnectTimer);
    try {
      this.es = new EventSource(`${this.base}/api/events`);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.es.onopen = () => {
      this.reconnectAttempt = 0;
      this.setSseState('open');
    };
    this.es.onmessage = (event) => {
      let message = null;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      if (message && message.type) this.emitEvent(message);
    };
    this.es.onerror = () => {
      this.setSseState('error');
      this.scheduleReconnect();
    };
  }

  scheduleReconnect() {
    if (this.closed) return;
    if (this.es) {
      try {
        this.es.close();
      } catch { /* ignore */ }
      this.es = null;
    }
    const attempt = Math.min(this.reconnectAttempt, 5);
    const delay = Math.min(30000, 2000 * Math.pow(2, attempt));
    this.reconnectAttempt += 1;
    clearTimeout(this.reconnectTimer);
    this.reconnectTimer = setTimeout(() => this.openEventSource(), delay);
  }

  disconnect() {
    this.closed = true;
    clearTimeout(this.reconnectTimer);
    if (this.es) {
      try {
        this.es.close();
      } catch { /* ignore */ }
      this.es = null;
    }
    this.setSseState('closed');
  }
}

/**
 * Factory: `?mock=1` → MockApi (fixtures + emulated SSE), otherwise the real
 * REST/SSE client. The mock module is imported lazily so the production path
 * never even fetches it.
 */
export async function createApi({ store } = {}) {
  const params = new URLSearchParams(window.location.search);
  const mock = params.get('mock') === '1' || params.get('mock') === 'true';
  if (mock) {
    const { MockApi } = await import('./mock/mock.js');
    const api = new MockApi({ store });
    await api.ready;
    return api;
  }
  return new ApiClient({ store, base: apiBaseFromLocation() });
}
