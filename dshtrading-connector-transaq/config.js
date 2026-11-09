// Connector configuration: defaults plus validation of the user-supplied part.

/** @typedef {{enabled: boolean, gatewayUrl: string, tokenRef: string, timeoutMs: number, pollMs: number}} Config */

export const DEFAULTS = Object.freeze({
  enabled: true,
  gatewayUrl: 'http://127.0.0.1:18090',
  tokenRef: 'TERMINAL_API_TOKEN',
  timeoutMs: 5000,
  pollMs: 2000,
});

const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '[::1]']);
const TOKEN_REF = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * Merge raw settings over DEFAULTS and validate them.
 * @param {Partial<Config>} [raw]
 * @returns {Config}
 */
export function resolveConfig(raw) {
  const cfg = { ...DEFAULTS };
  for (const [k, v] of Object.entries(raw ?? {})) {
    if (v !== undefined) cfg[k] = v;
  }
  let url;
  try {
    url = new URL(cfg.gatewayUrl);
  } catch {
    throw new TypeError('gatewayUrl must be a valid http:// loopback URL');
  }
  if (url.protocol !== 'http:' || !LOOPBACK_HOSTS.has(url.hostname)) {
    throw new TypeError('gatewayUrl must be an http:// loopback URL (127.0.0.1, localhost or [::1])');
  }
  if (typeof cfg.tokenRef !== 'string' || !TOKEN_REF.test(cfg.tokenRef)) {
    throw new TypeError('tokenRef must be an environment variable name');
  }
  for (const k of ['timeoutMs', 'pollMs']) {
    if (!Number.isFinite(cfg[k]) || cfg[k] <= 0) throw new TypeError(`${k} must be a positive number`);
  }
  cfg.enabled = Boolean(cfg.enabled);
  return cfg;
}
