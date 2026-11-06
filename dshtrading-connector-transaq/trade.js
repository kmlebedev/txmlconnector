// TradeService: dry-run order validation plus read-only paper account views.
// Gateway v1 is GET-only, so live placement is always rejected.

import { randomUUID } from 'node:crypto';
import { TradingError } from './gateway-client.js';
import { toCanonical } from './symbols.js';

const LIVE_OFF = 'live trading is disabled for the ru market (read-only gateway)';

function exchangeError(msg) {
  return new TradingError('TRADING_EXCHANGE_ERROR', msg);
}

function scaled(x, d) {
  return Math.round(Number(x) * 10 ** d);
}

/** Validate price against decimals and minStep using integers scaled by 10^decimals. */
function checkPrice(price, instrument) {
  const d = Number(instrument?.decimals ?? 2);
  const p = Number(price);
  if (!Number.isFinite(p) || p <= 0) throw exchangeError('limit order requires a positive price');
  const sp = scaled(p, d);
  if (Math.abs(p * 10 ** d - sp) > 1e-6) throw exchangeError(`price ${price} has more than ${d} decimal digits`);
  const step = scaled(instrument?.minStep ?? 1 / 10 ** d, d);
  if (step > 0 && sp % step !== 0) throw exchangeError(`price ${price} is not a multiple of price step ${instrument.minStep}`);
}

/**
 * @param {{client: {get: Function}, resolver: {resolve: Function}}} opts
 */
export function createTradeService({ client, resolver }) {
  const account = () => client.get('/account');

  async function placeOrder(req) {
    if (req?.dryRun === false) throw new TradingError('TRADING_LIVE_TRADING_DISABLED', LIVE_OFF);
    const { canonical, instrument } = await resolver.resolve(req?.symbol);
    if (req.side !== 'buy' && req.side !== 'sell') throw exchangeError('side must be buy or sell');
    if (req.type !== 'limit' && req.type !== 'market') throw exchangeError('type must be limit or market');
    const lot = Number(instrument?.lotSize) > 0 ? Number(instrument.lotSize) : 1;
    const qty = Number(req.quantity);
    if (!Number.isFinite(qty) || qty <= 0) throw exchangeError('quantity must be positive');
    if (!Number.isInteger(qty) || qty % lot !== 0) throw exchangeError(`quantity ${req.quantity} must be a multiple of lot size ${lot} shares`);
    if (req.type === 'limit') {
      if (req.price === undefined || req.price === null) throw exchangeError('limit order requires a price');
      checkPrice(req.price, instrument);
    }
    const order = {
      id: 'dry-' + randomUUID(),
      symbol: canonical,
      side: req.side,
      type: req.type,
      status: 'new',
      quantity: qty,
      filledQuantity: 0,
      dryRun: true,
      timestamp: Date.now(),
    };
    if (req.type === 'limit') order.price = Number(req.price);
    return order;
  }

  async function cancelOrder(id) {
    if (typeof id === 'string' && id.startsWith('dry-')) return;
    throw new TradingError('TRADING_LIVE_TRADING_DISABLED', LIVE_OFF);
  }

  const toOrder = (o) => {
    const out = {
      id: String(o.id),
      symbol: toCanonical(o.symbol),
      side: o.side,
      type: o.type,
      status: o.status,
      quantity: o.qty,
      filledQuantity: o.filled ?? 0,
      dryRun: false,
      timestamp: o.time,
    };
    const price = Number(o.price);
    if (o.price !== undefined && o.price !== '' && Number.isFinite(price)) out.price = price;
    return out;
  };

  async function listOpenOrders(symbol) {
    const [acc, want] = await Promise.all([account(), symbol ? resolver.resolve(symbol) : undefined]);
    return (acc.openOrders ?? []).filter((o) => !want || o.symbol === want.native).map(toOrder);
  }

  async function getOrder(symbol, id) {
    const orders = await listOpenOrders(symbol);
    const found = orders.find((o) => o.id === String(id));
    if (!found) throw new TradingError('TRADING_SYMBOL_NOT_FOUND', `order ${id} not found`);
    return found;
  }

  async function getPositions() {
    const acc = await account();
    return (acc.positions ?? [])
      .filter((p) => Number(p.qty) !== 0)
      .map((p) => ({
        symbol: toCanonical(p.symbol),
        side: Number(p.qty) > 0 ? 'long' : 'short',
        quantity: Math.abs(Number(p.qty)),
        entryPrice: Number(p.avgPrice),
        realizedPnl: Number(p.realizedPnl),
      }));
  }

  async function getBalances() {
    const acc = await account();
    const cash = Number(acc.cash);
    return [{ asset: 'RUB', free: cash, locked: 0, total: cash }];
  }

  async function listTradeFills(symbol, limit = 50) {
    const [acc, want] = await Promise.all([account(), symbol ? resolver.resolve(symbol) : undefined]);
    return (acc.fills ?? [])
      .filter((f) => !want || f.symbol === want.native)
      .slice(0, limit)
      .map((f) => ({
        id: String(f.id),
        orderId: String(f.orderId),
        symbol: toCanonical(f.symbol),
        side: f.side,
        price: Number(f.price),
        quantity: f.qty,
        fee: Number(f.fee),
        timestamp: f.time,
      }));
  }

  return { placeOrder, cancelOrder, getOrder, getPositions, getBalances, listOpenOrders, listTradeFills };
}
