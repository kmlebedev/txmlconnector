import test from 'node:test';
import assert from 'node:assert/strict';
import { startFakeGateway } from './fake-gateway.mjs';
import { createGatewayClient } from '../gateway-client.js';
import { createSymbolResolver } from '../symbols.js';
import { createTradeService } from '../trade.js';

const SBER = { symbol: 'TQBR:SBER', secCode: 'SBER', board: 'TQBR', name: 'Sberbank', shortName: 'Sber', decimals: 2, lotSize: 10, minStep: '0.01' };
const ACCOUNT = {
  cash: '1000000.50',
  positions: [{ symbol: 'TQBR:SBER', qty: 100, avgPrice: '300.5', realizedPnl: '12.5' }, { symbol: 'TQBR:GAZP', qty: -20, avgPrice: '150', realizedPnl: '0' }],
  openOrders: [{ id: 7, symbol: 'TQBR:SBER', side: 'buy', type: 'limit', status: 'new', qty: 10, filled: 0, price: '300.00', time: 5 }],
  fills: [{ id: 1, orderId: 7, symbol: 'TQBR:SBER', side: 'buy', qty: 10, price: '300.00', fee: '0.5', time: 6 }],
};

async function setup() {
  const gw = await startFakeGateway({ routes: { '/api/gw/v1/instruments': { instruments: [SBER] }, '/api/gw/v1/account': ACCOUNT } });
  const client = createGatewayClient({ config: { gatewayUrl: gw.url, timeoutMs: 2000 } });
  const resolver = createSymbolResolver(async () => (await client.get('/instruments')).instruments);
  return { gw, trade: createTradeService({ client, resolver }) };
}

test('placeOrder defaults to dry-run and validates lot multiple', async () => {
  const { gw, trade } = await setup();
  try {
    await assert.rejects(trade.placeOrder({ symbol: 'SBER', side: 'buy', type: 'limit', quantity: 15, price: 300 }), { code: 'TRADING_EXCHANGE_ERROR' });
    const o = await trade.placeOrder({ symbol: 'SBER', side: 'buy', type: 'limit', quantity: 20, price: 300.01 });
    assert.equal(o.dryRun, true);
    assert.equal(o.symbol, 'SBER.TQBR');
    assert.equal(o.status, 'new');
    assert.equal(o.filledQuantity, 0);
    assert.match(o.id, /^dry-/);
    const m = await trade.placeOrder({ symbol: 'SBER', side: 'sell', type: 'market', quantity: 10 });
    assert.equal('price' in m, false);
  } finally { await gw.close(); }
});

test('limit price off-step rejected (306.255 with step 0.01)', async () => {
  const { gw, trade } = await setup();
  try {
    await assert.rejects(trade.placeOrder({ symbol: 'SBER', side: 'buy', type: 'limit', quantity: 10, price: 306.255 }), { code: 'TRADING_EXCHANGE_ERROR' });
    await assert.rejects(trade.placeOrder({ symbol: 'SBER', side: 'buy', type: 'limit', quantity: 10 }), { code: 'TRADING_EXCHANGE_ERROR' });
    await trade.placeOrder({ symbol: 'SBER', side: 'buy', type: 'limit', quantity: 10, price: 306.25 });
  } finally { await gw.close(); }
});

test('dryRun false is rejected without network', async () => {
  const { gw, trade } = await setup();
  try {
    const before = gw.calls.length;
    await assert.rejects(trade.placeOrder({ symbol: 'SBER', side: 'buy', type: 'limit', quantity: 10, price: 300, dryRun: false }), { code: 'TRADING_LIVE_TRADING_DISABLED' });
    await assert.rejects(trade.cancelOrder('123'), { code: 'TRADING_LIVE_TRADING_DISABLED' });
    await trade.cancelOrder('dry-abc');
    assert.equal(gw.calls.length, before);
  } finally { await gw.close(); }
});

test('positions map sign to side', async () => {
  const { gw, trade } = await setup();
  try {
    assert.deepEqual(await trade.getPositions(), [
      { symbol: 'SBER.TQBR', side: 'long', quantity: 100, entryPrice: 300.5, realizedPnl: 12.5 },
      { symbol: 'GAZP.TQBR', side: 'short', quantity: 20, entryPrice: 150, realizedPnl: 0 },
    ]);
    assert.deepEqual(await trade.getBalances(), [{ asset: 'RUB', free: 1000000.5, locked: 0, total: 1000000.5 }]);
  } finally { await gw.close(); }
});

test('open orders, getOrder and fills', async () => {
  const { gw, trade } = await setup();
  try {
    const orders = await trade.listOpenOrders('SBER');
    assert.equal(orders.length, 1);
    assert.equal(orders[0].symbol, 'SBER.TQBR');
    assert.equal(orders[0].price, 300);
    assert.equal((await trade.getOrder('SBER', '7')).id, '7');
    await assert.rejects(trade.getOrder('SBER', '99'), { code: 'TRADING_SYMBOL_NOT_FOUND' });
    const fills = await trade.listTradeFills('SBER');
    assert.deepEqual(fills, [{ id: '1', orderId: '7', symbol: 'SBER.TQBR', side: 'buy', price: 300, quantity: 10, fee: 0.5, timestamp: 6 }]);
  } finally { await gw.close(); }
});

test('order status vocabulary passes through from gateway', async () => {
  const { gw, trade } = await setup();
  try {
    gw.routes = { ...gw.routes, '/api/gw/v1/account': { ...ACCOUNT, openOrders: [{ ...ACCOUNT.openOrders[0], status: 'partially_filled', filled: 5 }] } };
    assert.equal((await trade.listOpenOrders())[0].status, 'partially_filled');
    assert.equal((await trade.getOrder('SBER', '7')).status, 'partially_filled');
  } finally { await gw.close(); }
});
