import test from 'node:test';
import assert from 'node:assert/strict';
import { startFakeGateway } from './fake-gateway.mjs';
import { createGatewayClient } from '../gateway-client.js';
import { createSymbolResolver } from '../symbols.js';
import { createMarketDataService } from '../market-data.js';

const SBER = { symbol: 'TQBR:SBER', secCode: 'SBER', board: 'TQBR', name: 'Sberbank', shortName: 'Сбербанк', decimals: 2, lotSize: 10, minStep: '0.01' };
const GAZP = { symbol: 'TQBR:GAZP', secCode: 'GAZP', board: 'TQBR', name: 'Gazprom', shortName: '', decimals: 2, lotSize: 10, minStep: '0.01' };
const INFO = { version: '1', source: 'live', intervals: [{ interval: '1m', periodSec: 60, maxBars: 5000 }, { interval: '1d', periodSec: 86400, maxBars: 3000 }] };
const QUOTE = { last: '101.00', bid: '100.90', ask: '101.10', prevClose: '100.00', changePct: '1.00', volumeLots: 5, time: 1730823840000, source: 'live' };

async function setup(routes, pollMs = 10) {
  const gw = await startFakeGateway({ routes: { '/api/gw/v1/info': INFO, '/api/gw/v1/instruments': { instruments: [SBER, GAZP] }, ...routes } });
  const client = createGatewayClient({ config: { gatewayUrl: gw.url, timeoutMs: 2000 } });
  const resolver = createSymbolResolver(async () => (await client.get('/instruments')).instruments);
  const svc = createMarketDataService({ client, resolver, pollMs });
  return { gw, svc };
}

test('getTicker converts lots and canonical symbol', async () => {
  const { gw, svc } = await setup({ '/api/gw/v1/quotes': { quotes: { 'TQBR:SBER': QUOTE } } });
  try {
    const t = await svc.getTicker('SBER');
    assert.deepEqual(t, { symbol: 'SBER.TQBR', name: 'Сбербанк', price: 101, bid: 100.9, ask: 101.1, prevClose: 100, changePercent: 1, volume: 50, timestamp: 1730823840000, source: 'live' });
  } finally { await gw.close(); }
});

test('getTicker omits empty optional fields and missing quote is not found', async () => {
  const { gw, svc } = await setup({ '/api/gw/v1/quotes': { quotes: { 'TQBR:SBER': { last: '101.00', bid: '', ask: '', prevClose: '', changePct: '', volumeLots: 0, time: 5, source: 'seed' } } } });
  try {
    const t = await svc.getTicker('SBER.TQBR');
    for (const k of ['bid', 'ask', 'prevClose', 'changePercent']) assert.equal(k in t, false, k);
    assert.equal(t.source, 'seed');
    await assert.rejects(svc.getTicker('GAZP'), { code: 'TRADING_SYMBOL_NOT_FOUND' });
  } finally { await gw.close(); }
});

test('getTickers keeps input keys and omits unknown', async () => {
  const { gw, svc } = await setup({ '/api/gw/v1/quotes': { quotes: { 'TQBR:SBER': QUOTE } } });
  try {
    const out = await svc.getTickers(['SBER', 'NOPE', 'GAZP.TQBR']);
    assert.deepEqual(Object.keys(out), ['SBER']);
    assert.equal(out.SBER.symbol, 'SBER.TQBR');
    assert.equal(gw.calls.filter((c) => c.startsWith('/api/gw/v1/quotes')).length, 1);
  } finally { await gw.close(); }
});

test('getTickers chunks /quotes requests by 200 symbols and merges results', async () => {
  const instruments = Array.from({ length: 201 }, (_, i) => ({ ...SBER, symbol: `TQBR:S${i}`, secCode: `S${i}` }));
  const seen = [];
  const { gw, svc } = await setup({
    '/api/gw/v1/instruments': { instruments },
    '/api/gw/v1/quotes': (req, url) => {
      const syms = url.searchParams.get('symbols').split(',');
      seen.push(syms.length);
      return { status: 200, body: { quotes: Object.fromEntries(syms.map((s) => [s, QUOTE])) } };
    },
  });
  try {
    const out = await svc.getTickers(instruments.map((i) => i.secCode));
    assert.equal(Object.keys(out).length, 201);
    assert.deepEqual(seen, [200, 1]);
    assert.equal(gw.calls.filter((c) => c.startsWith('/api/gw/v1/quotes')).length, 2);
  } finally { await gw.close(); }
});

for (const [status, code] of [[503, 'TRADING_UPSTREAM_ERROR'], [401, 'TRADING_AUTH_FAILED']]) {
  test(`getTickers surfaces gateway ${status} after one /instruments call`, async () => {
    const { gw, svc } = await setup({ '/api/gw/v1/instruments': () => ({ status, body: { error: { code: 'x', message: 'down' } } }) });
    try {
      await assert.rejects(svc.getTickers(['SBER', 'GAZP']), { code });
      assert.equal(gw.calls.filter((c) => c.startsWith('/api/gw/v1/instruments')).length, 1);
    } finally { await gw.close(); }
  });
}

test('getKlines rejects 2h before network', async () => {
  const { gw, svc } = await setup({});
  try {
    await assert.rejects(svc.getKlines('SBER', '2h'), { code: 'TRADING_UNSUPPORTED_INTERVAL' });
    assert.equal(gw.calls.some((c) => c.includes('/candles')), false);
  } finally { await gw.close(); }
});

test('getKlines maps bars with lot volume and default limit', async () => {
  const { gw, svc } = await setup({ '/api/gw/v1/candles': { bars: [{ openTime: 1, closeTime: 60, open: '1.5', high: '2', low: '1', close: '1.8', volumeLots: 3 }] } });
  try {
    const k = await svc.getKlines('SBER.TQBR', '1m');
    assert.deepEqual(k, [{ openTime: 1, open: 1.5, high: 2, low: 1, close: 1.8, volume: 30, closeTime: 60 }]);
    const call = gw.calls.find((c) => c.includes('/candles'));
    assert.match(call, /symbol=TQBR%3ASBER/);
    assert.match(call, /interval=1m/);
    assert.match(call, /limit=500/);
    assert.deepEqual(await svc.klineCapabilities(), [{ interval: '1m', maxBars: 5000 }, { interval: '1d', maxBars: 3000 }]);
    assert.equal(gw.calls.filter((c) => c.endsWith('/info')).length, 1);
  } finally { await gw.close(); }
});

test('listInstruments maps canonical refs', async () => {
  const { gw, svc } = await setup({});
  try {
    const l = await svc.listInstruments();
    assert.deepEqual(l[0], { symbol: 'SBER.TQBR', name: 'Сбербанк', form: 'spot', assetClass: 'equity' });
    assert.equal(l[1].name, 'Gazprom');
  } finally { await gw.close(); }
});

test('orderbook amount is lots*lotSize', async () => {
  const { gw, svc } = await setup({ '/api/gw/v1/orderbook': { symbol: 'TQBR:SBER', time: 9, source: 'live', bids: [{ price: '306.25', lots: 10 }], asks: [{ price: '306.30', lots: 7 }] } });
  try {
    const b = await svc.getOrderbook('SBER');
    assert.deepEqual(b, { symbol: 'SBER.TQBR', bids: [{ price: 306.25, amount: 100 }], asks: [{ price: 306.3, amount: 70 }], timestamp: 9 });
  } finally { await gw.close(); }
});

test('getRecentTrades maps ticks', async () => {
  const { gw, svc } = await setup({ '/api/gw/v1/trades': { trades: [{ id: '0@5', time: 5, price: '101.00', lots: 2, side: 'buy' }] } });
  try {
    const t = await svc.getRecentTrades('SBER', 20);
    assert.deepEqual(t, [{ id: '0@5', symbol: 'SBER.TQBR', price: 101, amount: 20, side: 'buy', timestamp: 5 }]);
    assert.match(gw.calls.find((c) => c.includes('/trades')), /limit=20/);
  } finally { await gw.close(); }
});

test('subscribeTicker recovers after gateway restart', async () => {
  let n = 0;
  const { gw, svc } = await setup({
    '/api/gw/v1/quotes': () => (++n <= 2 ? { status: 503, body: { error: { code: 'unavailable', message: 'down' } } } : { body: { quotes: { 'TQBR:SBER': QUOTE } } }),
  });
  try {
    const got = [];
    const sub = svc.subscribeTicker('SBER', (t) => got.push(t));
    await new Promise((r) => setTimeout(r, 300));
    assert.equal(got.length, 1);
    assert.equal(got[0].price, 101);
    sub.dispose();
    // A poll already in flight at dispose time may still reach the fake
    // gateway; let it land, then assert that no new poll is scheduled.
    await new Promise((r) => setTimeout(r, 50));
    const after = gw.calls.length;
    await new Promise((r) => setTimeout(r, 100));
    assert.equal(gw.calls.length, after);
  } finally { await gw.close(); }
});
