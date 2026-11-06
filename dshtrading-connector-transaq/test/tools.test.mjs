import test from 'node:test';
import assert from 'node:assert/strict';
import { startFakeGateway } from './fake-gateway.mjs';
import { createGatewayClient } from '../gateway-client.js';
import { createSymbolResolver } from '../symbols.js';
import { createMarketDataService } from '../market-data.js';
import { createTradeService } from '../trade.js';
import { buildTools } from '../tools.js';

const SBER = { symbol: 'TQBR:SBER', secCode: 'SBER', board: 'TQBR', name: 'Sberbank', shortName: 'Sber', decimals: 2, lotSize: 10, minStep: '0.01' };
const GAZP = { symbol: 'TQBR:GAZP', secCode: 'GAZP', board: 'TQBR', name: 'Gazprom', shortName: '', decimals: 2, lotSize: 10, minStep: '0.01' };
const P = '/api/gw/v1';

async function setup() {
  const gw = await startFakeGateway({
    routes: {
      [`${P}/info`]: { version: '1', source: 'seed', intervals: [{ interval: '1m', periodSec: 60, maxBars: 5000 }] },
      [`${P}/instruments`]: { instruments: [SBER, GAZP] },
      [`${P}/quotes`]: { quotes: { 'TQBR:SBER': { last: '101.00', volumeLots: 5, time: 1, source: 'seed' } } },
      [`${P}/candles`]: { source: 'seed', bars: [{ openTime: 1, open: '1', high: '2', low: '1', close: '2', volumeLots: 3, closeTime: 2 }] },
      [`${P}/orderbook`]: { source: 'seed', bids: [{ price: '100', lots: 1 }], asks: [], time: 3 },
      [`${P}/trades`]: { source: 'seed', trades: [{ id: 1, price: '100', lots: 2, side: 'buy', time: 4 }] },
      [`${P}/account`]: { cash: '10', positions: [], openOrders: [], fills: [] },
    },
  });
  const client = createGatewayClient({ config: { gatewayUrl: gw.url, timeoutMs: 2000 } });
  const resolver = createSymbolResolver(async () => (await client.get('/instruments')).instruments);
  const tools = buildTools({ market: createMarketDataService({ client, resolver }), trade: createTradeService({ client, resolver }) });
  return { gw, tools, byName: Object.fromEntries(tools.map((t) => [t.name, t])) };
}

test('buildTools has no *_place_order or *_cancel_order', async () => {
  const { gw, tools } = await setup();
  try {
    assert.deepEqual(tools.map((t) => t.name).sort(), ['ru_get_klines', 'ru_get_orderbook', 'ru_get_paper_account', 'ru_get_ticker', 'ru_get_trades', 'ru_list_instruments']);
    assert.equal(tools.some((t) => /_(place|cancel)_order$/.test(t.name)), false);
    for (const t of tools) {
      assert.equal(typeof t.execute, 'function');
      assert.equal(t.parameters.type, 'object');
      assert.ok(t.description.length > 0);
    }
  } finally { await gw.close(); }
});

test('ru_get_ticker result carries source seed in offline fake', async () => {
  const { gw, byName } = await setup();
  try {
    assert.equal((await byName.ru_get_ticker.execute({ symbol: 'SBER' }, {})).source, 'seed');
    const k = await byName.ru_get_klines.execute({ symbol: 'SBER', interval: '1m' }, {});
    assert.equal(k.symbol, 'SBER.TQBR');
    assert.equal(k.source, 'seed');
    assert.equal(k.bars[0].volume, 30);
    assert.equal((await byName.ru_get_orderbook.execute({ symbol: 'SBER' }, {})).source, 'seed');
    const t = await byName.ru_get_trades.execute({ symbol: 'SBER' }, {});
    assert.equal(t.symbol, 'SBER.TQBR');
    assert.equal(t.source, 'seed');
    assert.equal(t.trades[0].amount, 20);
  } finally { await gw.close(); }
});

test('ru_list_instruments filters and ru_get_paper_account shape', async () => {
  const { gw, byName } = await setup();
  try {
    const r = await byName.ru_list_instruments.execute({ query: 'gaz' }, {});
    assert.deepEqual(r.instruments.map((i) => i.symbol), ['GAZP.TQBR']);
    assert.equal((await byName.ru_list_instruments.execute({}, {})).instruments.length, 2);
    const a = await byName.ru_get_paper_account.execute({}, {});
    assert.deepEqual(Object.keys(a).sort(), ['balances', 'fills', 'openOrders', 'positions']);
    assert.equal(a.balances[0].asset, 'RUB');
  } finally { await gw.close(); }
});
