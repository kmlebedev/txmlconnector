// Symbol vocabulary: canonical SECCODE.BOARD <-> gateway/native BOARD:SECCODE.

import { TradingError } from './gateway-client.js';

const TTL_MS = 60_000;
const PREFERRED_BOARD = 'TQBR';

/** 'SBER.TQBR' -> 'TQBR:SBER' */
export function toNative(canonical) {
  const i = canonical.lastIndexOf('.');
  if (i <= 0 || i === canonical.length - 1) throw new TypeError(`not a canonical symbol: ${canonical}`);
  return `${canonical.slice(i + 1).toUpperCase()}:${canonical.slice(0, i).toUpperCase()}`;
}

/** 'TQBR:SBER' -> 'SBER.TQBR' */
export function toCanonical(native) {
  const i = native.indexOf(':');
  if (i <= 0 || i === native.length - 1) throw new TypeError(`not a native symbol: ${native}`);
  return `${native.slice(i + 1).toUpperCase()}.${native.slice(0, i).toUpperCase()}`;
}

/**
 * @param {() => Promise<object[]>} loadInstruments gateway /instruments array
 * @param {{now?: () => number}} [opts]
 */
export function createSymbolResolver(loadInstruments, { now = Date.now } = {}) {
  let cache = null; // { at, byNative: Map, bySecCode: Map }

  async function refresh() {
    const list = await loadInstruments();
    const byNative = new Map();
    const bySecCode = new Map();
    for (const ins of list ?? []) {
      const native = String(ins.symbol).toUpperCase();
      byNative.set(native, ins);
      const code = String(ins.secCode).toUpperCase();
      if (!bySecCode.has(code)) bySecCode.set(code, []);
      bySecCode.get(code).push(ins);
    }
    cache = { at: now(), byNative, bySecCode };
    return cache;
  }

  /** Look up in a snapshot; returns an instrument, an array of ambiguous candidates, or undefined. */
  function lookup(c, key) {
    if (key.includes(':')) return c.byNative.get(key);
    if (key.includes('.')) {
      try { return c.byNative.get(toNative(key)); } catch { return undefined; }
    }
    const matches = c.bySecCode.get(key);
    if (!matches?.length) return undefined;
    if (matches.length === 1) return matches[0];
    return matches.find((m) => String(m.board).toUpperCase() === PREFERRED_BOARD) ?? matches;
  }

  async function resolve(input) {
    const key = String(input ?? '').trim().toUpperCase();
    if (!key) throw new TradingError('TRADING_SYMBOL_NOT_FOUND', 'empty symbol');
    const fresh = !(cache && now() - cache.at < TTL_MS);
    let c = fresh ? await refresh() : cache;
    let found = lookup(c, key);
    if (found === undefined && !fresh) {
      // Miss: the list may be stale, refresh once.
      c = await refresh();
      found = lookup(c, key);
    }
    if (found === undefined) throw new TradingError('TRADING_SYMBOL_NOT_FOUND', `unknown symbol: ${input}`);
    if (Array.isArray(found)) {
      const cands = found.map((m) => m.symbol).join(', ');
      throw new TradingError('TRADING_UNSUPPORTED_SYMBOL', `ambiguous symbol ${input}; use one of: ${cands}`);
    }
    return { native: found.symbol, canonical: toCanonical(found.symbol), instrument: found };
  }

  return { resolve };
}
