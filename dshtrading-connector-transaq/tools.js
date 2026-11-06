// Read-only ru_* tools for the agent. No order placement tools by design.

const SYMBOL = { type: 'string', description: 'Ticker such as SBER or canonical SECCODE.BOARD such as SBER.TQBR' };
const SRC = 'Result includes `source`: "live" or "seed" (offline demo snapshot, not real-time; do not present seed data as live).';

/**
 * @param {{market: object, trade: object}} deps
 */
export function buildTools({ market, trade }) {
  return [
    {
      name: 'ru_get_ticker',
      description: `Latest quote of a MOEX instrument: price in RUB, volume in shares. ${SRC}`,
      parameters: { type: 'object', properties: { symbol: SYMBOL }, required: ['symbol'], additionalProperties: false },
      execute: async ({ symbol }) => market.getTicker(symbol),
    },
    {
      name: 'ru_get_klines',
      description: `OHLCV candles of a MOEX instrument (prices in RUB, volume in shares). ${SRC}`,
      parameters: {
        type: 'object',
        properties: {
          symbol: SYMBOL,
          interval: { type: 'string', description: 'Candle interval, e.g. 1m, 1d' },
          limit: { type: 'integer', minimum: 1, description: 'Number of most recent bars' },
        },
        required: ['symbol', 'interval'],
        additionalProperties: false,
      },
      execute: async ({ symbol, interval, limit }) => {
        const { symbol: canonical, source, data } = await market.getKlinesWithSource(symbol, interval, limit);
        return { symbol: canonical, interval, source, bars: data };
      },
    },
    {
      name: 'ru_get_orderbook',
      description: `Order book (bids and asks) of a MOEX instrument: price in RUB, amount in shares. ${SRC}`,
      parameters: { type: 'object', properties: { symbol: SYMBOL }, required: ['symbol'], additionalProperties: false },
      execute: async ({ symbol }) => {
        const { symbol: canonical, source, data } = await market.getOrderbookWithSource(symbol);
        return { ...data, symbol: canonical, source };
      },
    },
    {
      name: 'ru_get_trades',
      description: `Recent market trades of a MOEX instrument: price in RUB, amount in shares. ${SRC}`,
      parameters: {
        type: 'object',
        properties: { symbol: SYMBOL, limit: { type: 'integer', minimum: 1, description: 'Max trades' } },
        required: ['symbol'],
        additionalProperties: false,
      },
      execute: async ({ symbol, limit }) => {
        const { symbol: canonical, source, data } = await market.getRecentTradesWithSource(symbol, limit);
        return { symbol: canonical, source, trades: data };
      },
    },
    {
      name: 'ru_list_instruments',
      description: 'List MOEX instruments (canonical SECCODE.BOARD symbols), optionally filtered by a case-insensitive substring of symbol or name. At most 200 results.',
      parameters: {
        type: 'object',
        properties: { query: { type: 'string', description: 'Case-insensitive substring of symbol or name' } },
        additionalProperties: false,
      },
      execute: async ({ query } = {}) => {
        const all = await market.listInstruments();
        const q = query ? String(query).toLowerCase() : '';
        const hit = q ? all.filter((i) => i.symbol.toLowerCase().includes(q) || (i.name ?? '').toLowerCase().includes(q)) : all;
        return { total: hit.length, instruments: hit.slice(0, 200) };
      },
    },
    {
      name: 'ru_get_paper_account',
      description: 'Read-only paper account: RUB balances, positions (shares), open orders and fills. Not a live brokerage account; orders cannot be placed through this connector. Data may be seed (offline demo).',
      parameters: { type: 'object', properties: {}, additionalProperties: false },
      execute: async () => {
        const [balances, positions, openOrders, fills] = await Promise.all([
          trade.getBalances(), trade.getPositions(), trade.listOpenOrders(), trade.listTradeFills(),
        ]);
        return { balances, positions, openOrders, fills };
      },
    },
  ];
}
