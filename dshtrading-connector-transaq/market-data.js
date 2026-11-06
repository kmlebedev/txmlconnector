// MarketDataService over gateway v1: converts gateway shapes (lots, BOARD:SECCODE,
// decimal strings) into dsh-trading style numbers, shares and canonical symbols.

import { TradingError } from './gateway-client.js';
import { toCanonical } from './symbols.js';

/** Number(str) for a decimal string; undefined when missing or empty. */
function num(v) {
  if (v === undefined || v === null || v === '') return undefined;
  const n = Number(v);
  return Number.isFinite(n) ? n : undefined;
}

function lotSizeOf(instrument) {
  const n = Number(instrument?.lotSize);
  return n > 0 ? n : 1;
}

/**
 * @param {{client: {get: Function}, resolver: {resolve: Function}, pollMs?: number}} opts
 */
export function createMarketDataService({ client, resolver, pollMs = 2000 }) {
  let infoPromise = null;
  function info() {
    if (!infoPromise) {
      infoPromise = client.get('/info').catch((err) => {
        infoPromise = null; // do not cache failures
        throw err;
      });
    }
    return infoPromise;
  }

  function toTicker(canonical, instrument, q) {
    const t = {
      symbol: canonical,
      name: instrument.shortName || instrument.name || undefined,
      price: num(q.last),
      bid: num(q.bid),
      ask: num(q.ask),
      volume: num(q.volumeLots) === undefined ? undefined : q.volumeLots * lotSizeOf(instrument),
      timestamp: q.time,
      prevClose: num(q.prevClose),
      changePercent: num(q.changePct),
      source: q.source,
    };
    for (const k of Object.keys(t)) if (t[k] === undefined) delete t[k];
    return t;
  }

  async function getTicker(symbol) {
    const { native, canonical, instrument } = await resolver.resolve(symbol);
    const res = await client.get('/quotes', { symbols: native });
    const q = res?.quotes?.[native];
    if (!q) throw new TradingError('TRADING_SYMBOL_NOT_FOUND', `no quote for ${symbol}`);
    return toTicker(canonical, instrument, q);
  }

  async function getTickers(symbols) {
    const resolved = [];
    for (const input of symbols) {
      try {
        resolved.push({ input, ...(await resolver.resolve(input)) });
      } catch {
        // unresolvable inputs are omitted
      }
    }
    const out = {};
    if (!resolved.length) return out;
    const natives = [...new Set(resolved.map((r) => r.native))];
    const res = await client.get('/quotes', { symbols: natives.join(',') });
    for (const r of resolved) {
      const q = res?.quotes?.[r.native];
      if (q) out[r.input] = toTicker(r.canonical, r.instrument, q);
    }
    return out;
  }

  async function klineCapabilities() {
    const i = await info();
    return (i.intervals ?? []).map((x) => ({ interval: x.interval, maxBars: x.maxBars }));
  }

  async function getKlines(symbol, interval, limit = 500) {
    const caps = await klineCapabilities();
    if (!caps.some((c) => c.interval === interval)) {
      throw new TradingError('TRADING_UNSUPPORTED_INTERVAL', `unsupported interval ${interval}; supported: ${caps.map((c) => c.interval).join(', ')}`);
    }
    const { native, instrument } = await resolver.resolve(symbol);
    const lot = lotSizeOf(instrument);
    const res = await client.get('/candles', { symbol: native, interval, limit });
    return (res.bars ?? []).map((b) => ({
      openTime: b.openTime,
      open: Number(b.open),
      high: Number(b.high),
      low: Number(b.low),
      close: Number(b.close),
      volume: (b.volumeLots ?? 0) * lot,
      closeTime: b.closeTime,
    }));
  }

  async function listInstruments() {
    const res = await client.get('/instruments');
    return (res.instruments ?? []).map((i) => ({
      symbol: toCanonical(i.symbol),
      name: i.shortName || i.name || undefined,
      form: 'spot',
      assetClass: 'equity',
    }));
  }

  async function getOrderbook(symbol) {
    const { native, canonical, instrument } = await resolver.resolve(symbol);
    const lot = lotSizeOf(instrument);
    const res = await client.get('/orderbook', { symbol: native });
    const level = (l) => ({ price: Number(l.price), amount: l.lots * lot });
    return { symbol: canonical, bids: (res.bids ?? []).map(level), asks: (res.asks ?? []).map(level), timestamp: res.time };
  }

  async function getRecentTrades(symbol, limit) {
    const { native, canonical, instrument } = await resolver.resolve(symbol);
    const lot = lotSizeOf(instrument);
    const res = await client.get('/trades', { symbol: native, limit });
    return (res.trades ?? []).map((t) => ({
      id: t.id,
      symbol: canonical,
      price: Number(t.price),
      amount: t.lots * lot,
      side: t.side === 'buy' || t.side === 'sell' ? t.side : 'unknown',
      timestamp: t.time,
    }));
  }

  function subscribeTicker(symbol, cb) {
    let stopped = false;
    let timer = null;
    let last = null;
    async function tick() {
      try {
        const t = await getTicker(symbol);
        if (!stopped && (!last || last.price !== t.price || last.timestamp !== t.timestamp)) {
          last = t;
          cb(t);
        }
      } catch {
        // keep polling; the gateway may be restarting
      }
      if (!stopped) timer = setTimeout(tick, pollMs);
    }
    timer = setTimeout(tick, 0);
    return {
      dispose() {
        stopped = true;
        if (timer) clearTimeout(timer);
        timer = null;
      },
    };
  }

  return { getTicker, getTickers, getKlines, klineCapabilities, listInstruments, getOrderbook, getRecentTrades, subscribeTicker };
}
