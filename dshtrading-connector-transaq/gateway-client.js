// Thin read-only HTTP client for the terminal gateway v1.

const API_PREFIX = '/api/gw/v1';

/** Error carrying a dsh-trading style TRADING_* code. */
export class TradingError extends Error {
  /**
   * @param {string} code
   * @param {string} message
   */
  constructor(code, message) {
    super(message);
    this.name = 'TradingServiceError';
    this.code = code;
  }
}

function codeForStatus(status) {
  if (status === 401) return 'TRADING_AUTH_FAILED';
  if (status === 404) return 'TRADING_SYMBOL_NOT_FOUND';
  if (status === 400) return 'TRADING_UNSUPPORTED_SYMBOL';
  return 'TRADING_UPSTREAM_ERROR';
}

/**
 * @param {{config: {gatewayUrl: string, timeoutMs: number},
 *   resolveToken?: () => (string|undefined|Promise<string|undefined>),
 *   fetchImpl?: typeof fetch}} opts
 */
export function createGatewayClient({ config, resolveToken, fetchImpl = fetch }) {
  const base = config.gatewayUrl.replace(/\/+$/, '') + API_PREFIX;

  /**
   * GET a gateway path (relative to /api/gw/v1) and return the parsed JSON body.
   * @param {string} path
   * @param {Record<string, unknown>} [query] undefined values are skipped
   * @param {AbortSignal} [signal]
   */
  async function get(path, query, signal) {
    const token = resolveToken ? await resolveToken() : undefined;
    const redact = (text) => (token ? String(text).split(token).join('***') : String(text));
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(query ?? {})) {
      if (v !== undefined && v !== null) params.append(k, String(v));
    }
    const qs = params.toString();
    const url = `${base}${path}${qs ? `?${qs}` : ''}`;
    const timeout = AbortSignal.timeout(config.timeoutMs);
    const headers = { accept: 'application/json' };
    if (token) headers['X-Terminal-Token'] = token;

    let res;
    try {
      res = await fetchImpl(url, { method: 'GET', headers, signal: signal ? AbortSignal.any([signal, timeout]) : timeout });
    } catch (err) {
      const why = timeout.aborted ? `timed out after ${config.timeoutMs} ms` : redact(err?.message ?? err);
      throw new TradingError('TRADING_NETWORK', `gateway request failed: ${why}`);
    }

    let text;
    try {
      text = await res.text();
    } catch (err) {
      const why = timeout.aborted ? `timed out after ${config.timeoutMs} ms` : redact(err?.message ?? err);
      throw new TradingError('TRADING_NETWORK', `gateway response failed: ${why}`);
    }
    let body;
    try {
      body = text ? JSON.parse(text) : undefined;
    } catch {
      body = undefined;
    }
    if (!res.ok) {
      const msg = body?.error?.message ?? `gateway returned HTTP ${res.status}`;
      throw new TradingError(codeForStatus(res.status), redact(msg));
    }
    if (body === undefined) {
      throw new TradingError('TRADING_UPSTREAM_ERROR', 'gateway returned a non-JSON body');
    }
    return body;
  }

  return { get };
}
