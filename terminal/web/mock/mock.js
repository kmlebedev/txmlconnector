// mock/mock.js — demo data layer for `?mock=1` only.
// Fixtures live next to this file (instruments/portfolio/orders/trades/backtests/
// strategies/autonomy/health), candles and order books are generated deterministically
// from them, and SSE events are emulated with timers. Never loaded without ?mock=1.

import { PERIODS, periodLabel, strategyTitle } from '../utils.js';

const FIXTURE_FILES = [
  'health', 'instruments', 'portfolio', 'orders', 'trades',
  'strategies', 'backtests', 'autonomy',
];

function mockError(code, message) {
  const error = new Error(message);
  error.code = code;
  return error;
}

function hashString(text) {
  let hash = 2166136261;
  for (let i = 0; i < text.length; i += 1) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function mulberry32(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6D2B79F5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const nowIso = () => new Date().toISOString();
const roundTo = (value, decimals) => {
  const factor = Math.pow(10, decimals);
  return Math.round(value * factor) / factor;
};
const isWeekend = (unixSec) => {
  const day = new Date(unixSec * 1000).getUTCDay();
  return day === 0 || day === 6;
};

const PRICE_DECIMALS = (price) => (price >= 1000 ? 1 : price >= 10 ? 2 : 3);

const MSK_OFFSET_SEC = 3 * 3600;
const SESSION_OPEN_MIN = 9 * 60 + 50;
const SESSION_CLOSE_MIN = 18 * 60 + 40;

function inTradingSession(unixSec) {
  const date = new Date((unixSec + MSK_OFFSET_SEC) * 1000);
  const weekday = date.getUTCDay();
  if (weekday === 0 || weekday === 6) return false;
  const minutes = date.getUTCHours() * 60 + date.getUTCMinutes();
  return minutes >= SESSION_OPEN_MIN && minutes <= SESSION_CLOSE_MIN;
}

/** Timestamps (ascending) inside MOEX equity sessions; charts show no night gaps. */
function tradingTimes(count, periodSec, nowSec) {
  const times = [];
  if (periodSec >= 86400) {
    let t = Math.floor((nowSec + MSK_OFFSET_SEC) / 86400) * 86400 - MSK_OFFSET_SEC + 10 * 3600;
    if (t > nowSec) t -= 86400;
    while (times.length < count) {
      const weekday = new Date((t + MSK_OFFSET_SEC) * 1000).getUTCDay();
      if (weekday !== 0 && weekday !== 6) times.unshift(t);
      t -= 86400;
    }
    return times;
  }
  let t = Math.floor(nowSec / periodSec) * periodSec;
  for (let guard = 0; guard < 100000; guard += 1) {
    if (inTradingSession(t)) break;
    t -= periodSec;
  }
  while (times.length < count) {
    if (inTradingSession(t)) times.unshift(t);
    t -= periodSec;
  }
  return times;
}

function generateCandles(seedText, periodSec, count, lastPrice, decimals) {
  const rnd = mulberry32(hashString(seedText));
  const barVol = Math.sqrt(periodSec / 60) * 0.0011;
  const nowSec = Math.floor(Date.now() / 1000);
  const times = tradingTimes(count, periodSec, nowSec);
  let price = lastPrice * (0.9 + rnd() * 0.05);
  const bars = [];
  for (let i = 0; i < count; i += 1) {
    const ret = (rnd() + rnd() + rnd() - 1.5) * barVol;
    const open = price;
    const close = Math.max(price * 0.5, open * (1 + ret));
    const high = Math.max(open, close) * (1 + rnd() * barVol * 0.9);
    const low = Math.min(open, close) * (1 - rnd() * barVol * 0.9);
    bars.push({
      t: times[i],
      o: open,
      h: high,
      l: low,
      c: close,
      v: Math.round((2500 + rnd() * 28000) * Math.sqrt(periodSec / 60)),
      _decimals: decimals,
    });
    price = close;
  }
  const scale = lastPrice / bars[bars.length - 1].c;
  const d = decimals ?? PRICE_DECIMALS(lastPrice);
  return bars.map((bar) => ({
    t: bar.t,
    o: roundTo(bar.o * scale, d),
    h: roundTo(bar.h * scale, d),
    l: roundTo(bar.l * scale, d),
    c: roundTo(bar.c * scale, d),
    v: bar.v,
  }));
}

function sma(values, period) {
  const out = new Array(values.length).fill(null);
  let sum = 0;
  for (let i = 0; i < values.length; i += 1) {
    sum += values[i];
    if (i >= period) sum -= values[i - period];
    if (i >= period - 1) out[i] = sum / period;
  }
  return out;
}

function rsi(values, period = 14) {
  if (values.length <= period) return 50;
  let gain = 0;
  let loss = 0;
  for (let i = values.length - period; i < values.length; i += 1) {
    const diff = values[i] - values[i - 1];
    if (diff >= 0) gain += diff; else loss -= diff;
  }
  if (loss === 0) return 100;
  const rs = gain / period / (loss / period);
  return 100 - 100 / (1 + rs);
}

function atr(bars, period = 14) {
  if (bars.length < 2) return 0;
  let sum = 0;
  const from = Math.max(1, bars.length - period);
  for (let i = from; i < bars.length; i += 1) {
    const prevClose = bars[i - 1].c;
    sum += Math.max(bars[i].h - bars[i].l, Math.abs(bars[i].h - prevClose), Math.abs(bars[i].l - prevClose));
  }
  return sum / (bars.length - from);
}

function stdev(values) {
  if (values.length < 2) return 0;
  const mean = values.reduce((a, b) => a + b, 0) / values.length;
  const variance = values.reduce((a, b) => a + (b - mean) * (b - mean), 0) / (values.length - 1);
  return Math.sqrt(variance);
}

/** Long-only strategy replay used by mock backtests/autonomy: no lookahead, fee on turnover. */
function replayStrategy(candles, strategyId, params = {}, initialCash = 1_000_000, feeBps = 5, slippageBps = 1) {
  const closes = candles.map((bar) => bar.c);
  const fee = feeBps / 10000;
  const slip = slippageBps / 10000;
  const wantLong = new Array(candles.length).fill(false);
  if (strategyId === 'sma_cross') {
    const fast = Math.max(2, Math.round(params.fast ?? 10));
    const slow = Math.max(fast + 1, Math.round(params.slow ?? 30));
    const fastMa = sma(closes, fast);
    const slowMa = sma(closes, slow);
    for (let i = 0; i < candles.length; i += 1) {
      const ready = fastMa[i] !== null && slowMa[i] !== null;
      const prevReady = i > 0 && fastMa[i - 1] !== null && slowMa[i - 1] !== null;
      wantLong[i] = ready && fastMa[i] > slowMa[i] && (!prevReady || fastMa[i - 1] <= slowMa[i - 1]);
    }
  } else if (strategyId === 'rsi_reversion') {
    const period = Math.max(2, Math.round(params.period ?? 14));
    const oversold = params.oversold ?? 30;
    for (let i = period; i < candles.length; i += 1) {
      const value = rsi(closes.slice(0, i + 1), period);
      wantLong[i] = value < oversold;
    }
  } else if (strategyId === 'donchian_breakout') {
    const period = Math.max(3, Math.round(params.period ?? 20));
    for (let i = period; i < candles.length; i += 1) {
      const window = candles.slice(i - period, i);
      const maxHigh = Math.max(...window.map((bar) => bar.h));
      wantLong[i] = closes[i] > maxHigh;
    }
  } else if (strategyId === 'momentum') {
    const lookback = Math.max(2, Math.round(params.lookback ?? 20));
    const threshold = (params.thresholdPct ?? 1) / 100;
    for (let i = lookback; i < candles.length; i += 1) {
      wantLong[i] = closes[i] / closes[i - lookback] - 1 > threshold;
    }
  } else {
    throw mockError('bad_request', `Неизвестная стратегия: ${strategyId}`);
  }
  let cash = initialCash;
  let qty = 0;
  let entryPrice = 0;
  let entryTime = null;
  let entryIndex = 0;
  const trades = [];
  const equity = [];
  let peak = initialCash;
  let maxDrawdown = 0;
  let exposureBars = 0;
  const barReturns = [];
  let benchmark = candles.length ? closes[0] : 0;

  for (let i = 0; i < candles.length; i += 1) {
    // actions on close of i, execution on open of the next bar
    const next = candles[i + 1];
    if (next) {
      if (wantLong[i] && qty === 0) {
        const fill = next.o * (1 + slip);
        const affordable = Math.floor((cash * 0.98) / (fill * (1 + fee)));
        if (affordable > 0) {
          qty = affordable;
          cash -= qty * fill * (1 + fee);
          entryPrice = fill;
          entryTime = next.t;
          entryIndex = i + 1;
        }
      } else if (!wantLong[i] && qty > 0) {
        const fill = next.o * (1 - slip);
        const proceeds = qty * fill * (1 - fee);
        cash += proceeds;
        const pnl = proceeds - qty * entryPrice * (1 + fee);
        trades.push({
          entryTime: new Date(entryTime * 1000).toISOString(),
          exitTime: new Date(next.t * 1000).toISOString(),
          side: 'long',
          entryPrice: roundTo(entryPrice, 2),
          exitPrice: roundTo(fill, 2),
          qty,
          pnl: roundTo(pnl, 2),
          pnlPct: roundTo((fill / (entryPrice * (1 + fee)) - 1) * 100, 2),
          barsHeld: i + 1 - entryIndex,
          reason: 'сигнал стратегии',
        });
        qty = 0;
        entryPrice = 0;
      }
    }
    const mark = candles[i].c;
    const value = cash + qty * mark;
    equity.push(value);
    if (i > 0) barReturns.push(value / equity[i - 1] - 1);
    peak = Math.max(peak, value);
    maxDrawdown = Math.min(maxDrawdown, (value / peak - 1) * 100);
    if (qty > 0) exposureBars += 1;
    benchmark = closes[0] ? mark / closes[0] : 1;
  }
  // close an open position at the last close for reporting
  if (qty > 0) {
    const last = candles[candles.length - 1];
    const fill = last.c * (1 - slip);
    const proceeds = qty * fill * (1 - fee);
    cash += proceeds;
    const pnl = proceeds - qty * entryPrice * (1 + fee);
    trades.push({
      entryTime: new Date(entryTime * 1000).toISOString(),
      exitTime: new Date(last.t * 1000).toISOString(),
      side: 'long',
      entryPrice: roundTo(entryPrice, 2),
      exitPrice: roundTo(fill, 2),
      qty,
      pnl: roundTo(pnl, 2),
      pnlPct: roundTo((fill / (entryPrice * (1 + fee)) - 1) * 100, 2),
      barsHeld: candles.length - 1 - entryIndex,
      reason: 'закрытие по последнему бару',
    });
    qty = 0;
  }
  const finalEquity = cash;
  const finalCloses = candles[candles.length - 1];
  const initialBar = candles[0];
  const wins = trades.filter((trade) => trade.pnl > 0);
  const losses = trades.filter((trade) => trade.pnl <= 0);
  const grossWin = wins.reduce((sum, trade) => sum + trade.pnl, 0);
  const grossLoss = Math.abs(losses.reduce((sum, trade) => sum + trade.pnl, 0));
  const annualization = Math.sqrt((365 * 24 * 3600) / (candles.length > 1
    ? (candles[candles.length - 1].t - candles[0].t) / (candles.length - 1) : 300));
  const std = stdev(barReturns);
  const mean = barReturns.length
    ? barReturns.reduce((a, b) => a + b, 0) / barReturns.length : 0;
  const sharpe = std > 0 ? (mean / std) * annualization : 0;
  // Downsample equity to ≤ 220 points for the UI payload.
  const steps = Math.max(1, Math.ceil(candles.length / 220));
  const equityPoints = [];
  let dd = 0;
  let peakPoint = initialCash;
  for (let i = 0; i < candles.length; i += steps) {
    const value = equity[i];
    peakPoint = Math.max(peakPoint, value);
    dd = Math.min(dd, (value / peakPoint - 1) * 100);
    equityPoints.push({
      time: new Date(candles[i].t * 1000).toISOString(),
      equity: roundTo(value, 2),
      benchmark: roundTo(initialCash * (closes[i] / initialBar.c), 2),
      drawdownPct: roundTo(dd, 2),
    });
  }
  const warnings = [];
  if (candles.length < 50) warnings.push('мало баров для устойчивых выводов');
  if (!trades.length) warnings.push('стратегия не совершила ни одной сделки');
  if (maxDrawdown < -20) warnings.push('высокая максимальная просадка');
  return {
    symbol: '',
    strategy: strategyId,
    periodSec: 0,
    params: Object.assign({}, params),
    metrics: {
      initialCash,
      finalEquity: roundTo(finalEquity, 2),
      totalReturnPct: roundTo((finalEquity / initialCash - 1) * 100, 2),
      benchmarkReturnPct: roundTo((finalCloses.c / initialBar.c - 1) * 100, 2),
      maxDrawdownPct: roundTo(maxDrawdown, 2),
      sharpe: roundTo(Math.max(-9.99, Math.min(9.99, sharpe)), 2),
      winRatePct: trades.length ? roundTo((wins.length / trades.length) * 100, 1) : 0,
      profitFactor: grossLoss > 0 ? roundTo(grossWin / grossLoss, 2) : (grossWin > 0 ? 99 : 0),
      avgTradePct: trades.length
        ? roundTo(trades.reduce((sum, trade) => sum + trade.pnlPct, 0) / trades.length, 2) : 0,
      exposurePct: roundTo((exposureBars / Math.max(1, candles.length)) * 100, 1),
      trades: trades.length,
      bars: candles.length,
      from: new Date(candles[0].t * 1000).toISOString(),
      to: new Date(candles[candles.length - 1].t * 1000).toISOString(),
    },
    equity: equityPoints,
    trades,
    warnings,
  };
}

export class MockApi {
  constructor({ store = null } = {}) {
    this.store = store;
    this.isMock = true;
    this.data = {};
    this.listeners = new Set();
    this.candleCache = new Map();
    this.closed = true;
    this.tickTimer = 0;
    this.slowTimer = 0;
    this.autonomyTimer = 0;
    this.autonomyState = null;
    this.autonomyStep = 0;
    this.extraBacktests = [];
    this.orderSeq = 100;
    this.ready = this.init();
  }

  async init() {
    const loaded = await Promise.all(FIXTURE_FILES.map(async (name) => {
      const response = await fetch(`./mock/${name}.json`, { cache: 'no-store' });
      if (!response.ok) throw new Error(`mock fixture ./mock/${name}.json: HTTP ${response.status}`);
      return [name, await response.json()];
    }));
    for (const [name, data] of loaded) this.data[name] = data;
    this.instrumentsData = this.data.instruments.instruments;
    this.portfolioData = this.data.portfolio;
    this.ordersData = this.data.orders.orders;
    this.tradeHistoryData = this.data.trades.trades;
    this.autonomyState = JSON.parse(JSON.stringify(this.data.autonomy.autonomy));
    this.autoLog = this.autonomyState.log;
    this.healthData = this.data.health;
    this.bySymbol = new Map(this.instrumentsData.map((inst) => [inst.symbol, inst]));
    this.marketOpen = true;
    return this;
  }

  // ---- helpers ---------------------------------------------------------

  instrument(symbol) {
    const inst = this.bySymbol.get(symbol);
    if (!inst) throw mockError('not_found', `Инструмент не найден: ${symbol}`);
    return inst;
  }

  statusPayload() {
    return {
      ok: true,
      mode: 'offline',
      connected: true,
      message: 'мок-данные (бэкенд не подключён)',
      serverTime: nowIso(),
    };
  }

  candlesFor(symbol, periodSec, count) {
    const inst = this.instrument(symbol);
    const key = `${symbol}|${periodSec}|${count}`;
    const cached = this.candleCache.get(key);
    if (cached && Math.abs(cached.last - inst.last) / inst.last < 0.0015) return cached.bars;
    const bars = generateCandles(`${symbol}|${periodSec}`, periodSec, count, inst.last, inst.decimals);
    this.candleCache.set(key, { last: inst.last, bars });
    return bars;
  }

  synthBacktest({ symbol, periodSec = 300, strategy = 'sma_cross', params = {}, initialCash = 1_000_000, feeBps = 5, slippageBps = 1, count = 420 }) {
    const bars = this.candlesFor(symbol, periodSec, Math.max(60, Math.min(Number(count) || 420, 1200)));
    const result = replayStrategy(bars, strategy, params, initialCash, feeBps, slippageBps);
    result.symbol = symbol;
    result.periodSec = periodSec;
    return result;
  }

  // ---- REST surface ----------------------------------------------------

  async health() {
    await this.ready;
    return Object.assign({}, this.healthData, { serverTime: nowIso() });
  }

  async instruments() {
    await this.ready;
    return { serverTime: nowIso(), instruments: this.instrumentsData };
  }

  async candles(symbol, periodSec, count = 400) {
    await this.ready;
    const bars = this.candlesFor(symbol, periodSec, Math.max(30, Math.min(Number(count) || 400, 1200)));
    return {
      symbol,
      periodSec,
      source: 'seed',
      note: 'демо-снапшот терминала (мок)',
      candles: bars,
    };
  }

  async orderbook(symbol) {
    await this.ready;
    const inst = this.instrument(symbol);
    const rnd = mulberry32(hashString(`${symbol}|book|${inst.last}`));
    const step = inst.minStep || 0.01;
    const decimals = inst.decimals;
    const bids = [];
    const asks = [];
    for (let i = 0; i < 15; i += 1) {
      bids.push({
        price: roundTo(inst.last - step * (i + 1), decimals),
        qty: Math.round(20 + rnd() * 2400),
      });
      asks.push({
        price: roundTo(inst.last + step * (i + 1), decimals),
        qty: Math.round(20 + rnd() * 2400),
      });
    }
    return {
      symbol,
      last: inst.last,
      time: nowIso(),
      bids,
      asks,
      source: 'seed',
    };
  }

  async trades(symbol, limit = 60) {
    await this.ready;
    const inst = this.instrument(symbol);
    const rnd = mulberry32(hashString(`${symbol}|trades`));
    const count = Math.min(Number(limit) || 60, 200);
    const rows = [];
    let t = Date.now();
    for (let i = 0; i < count; i += 1) {
      t -= Math.round(1500 + rnd() * 9000);
      const price = roundTo(inst.last + (rnd() - 0.5) * inst.minStep * 6, inst.decimals);
      rows.push({
        time: new Date(t).toISOString(),
        price,
        qty: Math.round(1 + rnd() * 40) * (inst.lotSize || 1),
        side: rnd() > 0.5 ? 'buy' : 'sell',
      });
    }
    return rows;
  }

  async hints(symbol, periodSec) {
    await this.ready;
    const inst = this.instrument(symbol);
    const bars = this.candlesFor(symbol, periodSec, 320);
    const closes = bars.map((bar) => bar.c);
    const sma20 = sma(closes, 20);
    const sma50 = sma(closes, 50);
    const last = closes[closes.length - 1];
    const s20 = sma20[sma20.length - 1] || last;
    const s50 = sma50[sma50.length - 1] || last;
    const trend = last > s20 && s20 > s50 ? 'up' : last < s20 && s20 < s50 ? 'down' : 'flat';
    const recent = bars.slice(-60);
    const support = Math.min(...recent.map((bar) => bar.l));
    const resistance = Math.max(...recent.map((bar) => bar.h));
    const returns = [];
    for (let i = 1; i < bars.length; i += 1) returns.push(bars[i].c / bars[i - 1].c - 1);
    let advancers = 0;
    let decliners = 0;
    let unchanged = 0;
    for (const other of this.instrumentsData) {
      if (other.changePct > 0.05) advancers += 1;
      else if (other.changePct < -0.05) decliners += 1;
      else unchanged += 1;
    }
    return {
      symbol,
      periodSec,
      asOf: nowIso(),
      sma20: roundTo(s20, inst.decimals),
      sma50: roundTo(s50, inst.decimals),
      rsi14: roundTo(rsi(closes, 14), 1),
      atr14: roundTo(atr(bars, 14), inst.decimals),
      volatilityPct: roundTo(stdev(returns) * 100, 3),
      trend,
      support: roundTo(support, inst.decimals),
      resistance: roundTo(resistance, inst.decimals),
      breadth: { advancers, decliners, unchanged },
      note: 'Локальный аналитик по демо-данным',
    };
  }

  async portfolio() {
    await this.ready;
    return JSON.parse(JSON.stringify(this.portfolioData));
  }

  async orders() {
    await this.ready;
    return JSON.parse(JSON.stringify(this.ordersData));
  }

  async tradeHistory() {
    await this.ready;
    return JSON.parse(JSON.stringify(this.tradeHistoryData));
  }

  async placeOrder(payload) {
    await this.ready;
    const symbol = payload && payload.symbol;
    const inst = this.instrument(symbol);
    const side = payload.side === 'sell' ? 'sell' : 'buy';
    const type = payload.type === 'limit' ? 'limit' : 'market';
    const qty = Math.max(1, Math.round(Number(payload.qty) || 0));
    if (!qty) throw mockError('bad_request', 'Укажите количество');
    const limits = this.portfolioData;
    const price = type === 'market' ? (side === 'buy' ? inst.ask : inst.bid) : roundTo(Number(payload.price) || inst.last, inst.decimals);
    if (!price || !Number.isFinite(price)) throw mockError('bad_request', 'Некорректная цена заявки');
    const cost = qty * price;
    if (side === 'buy' && cost > limits.cash) throw mockError('conflict', 'Недостаточно свободных денег');
    const position = (limits.positions || []).find((row) => row.symbol === symbol);
    if (side === 'sell' && (!position || position.qty < qty)) throw mockError('conflict', 'Продажа больше позиции (short не поддерживается)');
    const marketable = type === 'market'
      || (side === 'buy' && inst.ask <= price)
      || (side === 'sell' && inst.bid >= price);
    this.orderSeq += 1;
    const id = `ord_${String(this.orderSeq).padStart(6, '0')}`;
    const timestamp = nowIso();
    const order = {
      id,
      symbol,
      side,
      type,
      qty,
      filled: marketable ? qty : 0,
      price: type === 'market' ? 0 : price,
      avgFillPrice: marketable ? price : 0,
      status: marketable ? 'filled' : 'new',
      createdAt: timestamp,
      updatedAt: timestamp,
      source: payload.source || 'ui',
      message: '',
    };
    this.ordersData.unshift(order);
    if (marketable) {
      const fee = roundTo(qty * price * 0.0005, 2);
      this.tradeHistoryData.unshift({
        id: `trd_${String(this.orderSeq + 100).padStart(6, '0')}`,
        orderId: id,
        symbol,
        side,
        qty,
        price,
        fee,
        time: timestamp,
        source: payload.source || 'ui',
      });
      this.applyFill(order, price, fee);
    }
    this.emitMsg({ type: 'orders', data: { orders: this.ordersData } });
    this.emitMsg({ type: 'portfolio', data: this.portfolioData });
    return order;
  }

  applyFill(order, price, fee) {
    const symbol = order.symbol;
    const side = order.side;
    const qty = order.qty;
    const portfolio = this.portfolioData;
    let position = portfolio.positions.find((row) => row.symbol === symbol);
    if (side === 'buy') {
      portfolio.cash = roundTo(portfolio.cash - qty * price - fee, 2);
      if (position) {
        const totalQty = position.qty + qty;
        position.avgPrice = roundTo((position.avgPrice * position.qty + price * qty) / totalQty, 4);
        position.qty = totalQty;
      } else {
        const inst = this.instrument(symbol);
        portfolio.positions.push({
          symbol, name: inst.name, qty, avgPrice: price, price, value: roundTo(price * qty, 2),
          dayChangePct: inst.changePct, profit: 0, profitPct: 0, realizedPnl: 0,
        });
        position = portfolio.positions[portfolio.positions.length - 1];
      }
    } else if (position) {
      portfolio.cash = roundTo(portfolio.cash + qty * price - fee, 2);
      const realized = roundTo((price - position.avgPrice) * qty, 2);
      position.realizedPnl = roundTo((position.realizedPnl || 0) + realized, 2);
      portfolio.realizedPnl = roundTo((portfolio.realizedPnl || 0) + realized, 2);
      position.qty -= qty;
      if (position.qty <= 0) portfolio.positions = portfolio.positions.filter((row) => row !== position);
    }
    portfolio.fees = roundTo((portfolio.fees || 0) + fee, 2);
    portfolio.updatedAt = nowIso();
    this.recomputePortfolio();
  }

  recomputePortfolio() {
    const portfolio = this.portfolioData;
    let value = 0;
    let day = 0;
    for (const position of portfolio.positions) {
      const inst = this.bySymbol.get(position.symbol);
      if (!inst) continue;
      position.price = inst.last;
      position.value = roundTo(inst.last * position.qty, 2);
      position.profit = roundTo((inst.last - position.avgPrice) * position.qty, 2);
      position.profitPct = position.avgPrice ? roundTo((inst.last / position.avgPrice - 1) * 100, 2) : 0;
      position.dayChangePct = inst.changePct;
      value += position.value;
      day += (inst.last - inst.prevClose) * position.qty;
    }
    portfolio.totalValue = roundTo(value + portfolio.cash, 2);
    portfolio.dayChange = roundTo(day, 2);
    const prevValue = portfolio.totalValue - day;
    portfolio.dayChangePct = prevValue ? roundTo((day / prevValue) * 100, 2) : 0;
    portfolio.updatedAt = nowIso();
  }

  async cancelOrder(id) {
    await this.ready;
    const order = this.ordersData.find((row) => row.id === id);
    if (!order) throw mockError('not_found', 'Заявка не найдена');
    if (order.status !== 'new') throw mockError('conflict', 'Заявку нельзя отменить');
    order.status = 'cancelled';
    order.updatedAt = nowIso();
    order.message = 'отменена пользователем';
    this.emitMsg({ type: 'orders', data: { orders: this.ordersData } });
    return { ok: true, order };
  }

  async strategies() {
    await this.ready;
    return this.data.strategies.strategies;
  }

  async backtest(payload) {
    await this.ready;
    return this.synthBacktest(payload || {});
  }

  async agentTools() {
    await this.ready;
    const rows = [
      ['list_instruments', 'Список инструментов', 'Котировки всех инструментов терминала'],
      ['get_market_hints', 'Рыночные подсказки', 'SMA/RSI/ATR, тренд, уровни, ширина рынка'],
      ['get_history', 'История баров', 'Свечи выбранного периода'],
      ['run_backtest', 'Бэктест стратегии', 'Прогон стратегии на истории с метриками'],
      ['compare_strategies', 'Сравнить стратегии', 'Ранжирование стратегий по доходности'],
      ['get_quote', 'Котировка', 'Цена, изменение и стакан по инструменту'],
      ['get_portfolio', 'Портфель', 'Стоимость, кэш, позиции и PnL'],
      ['place_order', 'Бумажная заявка', 'Заявка на бумажный счёт с риск-лимитами'],
      ['cancel_order', 'Отменить заявку', 'Снятие активной бумажной заявки'],
      ['start_autonomy', 'Запуск автономии', 'Replay-автоторговля на бумажном счёте'],
      ['stop_autonomy', 'Остановка автономии', 'Kill-switch автономии'],
      ['get_agent_status', 'Статус агента', 'Состояние LLM и автономии'],
      ['get_backtest_history', 'История бэктестов', 'Последние результаты агента'],
    ];
    return rows.map(([name, title, description]) => ({
      name, title, description, input: { type: 'object', properties: {} },
    }));
  }

  async agentChat(payload) {
    await this.ready;
    await sleep(420 + Math.random() * 480);
    const message = String((payload && payload.message) || '');
    const text = message.toLowerCase();
    const state = this.store ? this.store.get() : {};
    const symbol = (state.selected && this.bySymbol.get(state.selected)) ? state.selected : 'TQBR:SBER';
    const inst = this.instrument(symbol);
    const periodSec = state.periodSec || 300;
    const startedAt = performance.now();
    const toolCalls = [];
    const artifacts = [];
    const tool = (name, args, result) => {
      toolCalls.push({
        name,
        args,
        result,
        error: '',
        durationMs: Math.round(4 + Math.random() * 45),
      });
    };
    let answer;

    const wantsAutonomy = /(запуст|старт|включ|торгуй|автоторг)/.test(text)
      && /(авто|торгов|робот|replay)/.test(text);
    const wantsBacktest = /(бэктест|бектест|стратег|протест|тест|доходн|прибыльн)/.test(text);
    const wantsPortfolio = /(портфел|позици|кэш|деньг|убыт|итог)/.test(text);
    const wantsQuote = /(стакан|котиров|цена|сделк|заявк)/.test(text);

    if (wantsAutonomy) {
      const autonomy = this.startAutonomy({
        symbol,
        periodSec,
        strategy: 'sma_cross',
        params: { fast: 10, slow: 30 },
        speed: 5,
        budget: 200000,
        maxPositionPct: 25,
        stopLossPct: 2,
        takeProfitPct: 5,
        dailyLossPct: 3,
        live: false,
      });
      tool('start_autonomy', { symbol, strategy: 'sma_cross', budget: 200000 }, { running: true, config: autonomy.config });
      artifacts.push({ kind: 'autonomy', payload: autonomy });
      answer = `Запустил автоторговлю на бумажном счёте: ${inst.name} (${inst.symbol}), период ${periodLabel(periodSec)}, стратегия «${strategyTitle('sma_cross')}», скорость 5 баров/с, бюджет 200 000 ₽. ` +
        'Риск-лимиты: макс. 25% портфеля в позиции, стоп-лосс 2%, тейк-профит 5%, дневной лимит убытка 3%. ' +
        'Это replay по истории — реальные ордера не отправляются. Статус и лог — на вкладке «Автономия».';
    } else if (wantsBacktest) {
      const strategyIds = ['sma_cross', 'rsi_reversion', 'donchian_breakout', 'momentum'];
      const ranked = strategyIds.map((id) => {
        const result = this.synthBacktest({
          symbol, periodSec, strategy: id,
          params: id === 'sma_cross' ? { fast: 10, slow: 30 } : id === 'donchian_breakout' ? { period: 20 } : {},
          count: 420,
        });
        return {
          strategy: id,
          title: strategyTitle(id),
          metrics: {
            totalReturnPct: result.metrics.totalReturnPct,
            maxDrawdownPct: result.metrics.maxDrawdownPct,
            sharpe: result.metrics.sharpe,
            winRatePct: result.metrics.winRatePct,
            trades: result.metrics.trades,
          },
          result,
        };
      }).sort((a, b) => b.metrics.totalReturnPct - a.metrics.totalReturnPct);
      const best = ranked[0];
      // reuse the real computed result for the artifact
      best.result.symbol = symbol;
      best.result.periodSec = periodSec;
      this.extraBacktests.unshift(best.result);
      if (this.extraBacktests.length > 20) this.extraBacktests.pop();
      tool('get_market_hints', { symbol, periodSec }, { trend: 'up', note: 'SMA/RSI по истории' });
      tool('compare_strategies', { symbol, periodSec, count: 420 }, {
        ranked: ranked.map((row) => ({ strategy: row.strategy, metrics: row.metrics })),
        best: { strategy: best.strategy },
      });
      artifacts.push({ kind: 'backtest', payload: best.result });
      const m = best.result.metrics;
      answer = `Прогнал 4 стратегии на ${inst.name} (${inst.symbol}), период ${periodLabel(periodSec)}, ${m.bars} баров. ` +
        `Лучший результат — «${strategyTitle(best.strategy)}»: доходность ${m.totalReturnPct > 0 ? '+' : ''}${m.totalReturnPct}% ` +
        `против ${m.benchmarkReturnPct > 0 ? '+' : ''}${m.benchmarkReturnPct}% у бенчмарка (buy & hold). ` +
        `Сделок ${m.trades}, винрейт ${m.winRatePct}%, макс. просадка ${m.maxDrawdownPct}%. ` +
        'Учтите: комиссия 0,05% и проскальзывание 1 б.п. учтены, прошлая доходность не гарантирует будущую. ' +
        'Карточка бэктеста ниже — можно наложить equity и сделки на график.';
    } else if (wantsPortfolio) {
      const portfolio = this.portfolioData;
      tool('get_portfolio', {}, {
        totalValue: portfolio.totalValue, cash: portfolio.cash,
        dayChange: portfolio.dayChange, positions: portfolio.positions.length,
      });
      answer = `Портфель: ${portfolio.positions.length} позиций, стоимость ${portfolio.totalValue.toLocaleString('ru-RU', { minimumFractionDigits: 2 })} ₽, ` +
        `свободные деньги ${portfolio.cash.toLocaleString('ru-RU', { minimumFractionDigits: 2 })} ₽. ` +
        `За день ${portfolio.dayChange >= 0 ? '+' : ''}${portfolio.dayChange.toLocaleString('ru-RU', { minimumFractionDigits: 2 })} ₽ ` +
        `(${portfolio.dayChangePct > 0 ? '+' : ''}${portfolio.dayChangePct}%). ` +
        'Крупнейшие позиции видно в нижней панели на вкладке «Портфель». Риск: концентрация в одной бумаге ограничена 25%.';
    } else if (wantsQuote) {
      const book = await this.orderbook(symbol);
      tool('get_quote', { symbol }, {
        symbol, last: inst.last, bid: inst.bid, ask: inst.ask,
        changePct: inst.changePct,
      });
      const spread = roundTo(inst.ask - inst.bid, inst.decimals);
      answer = `${inst.name} (${inst.symbol}): последняя ${inst.last} ₽, изменение ${inst.changePct > 0 ? '+' : ''}${inst.changePct}%. ` +
        `Спред ${spread} ₽ (${inst.bid} / ${inst.ask}), дневной объём ${(inst.volume / 1e6).toFixed(1)} млн бумаг. ` +
        'В стакане наблюдаются локальные уровни — смотрите вкладку «Стакан» справа от графика.';
      artifacts.push({ kind: 'orderbook', payload: book });
    } else {
      const hints = await this.hints(symbol, periodSec);
      tool('get_market_hints', { symbol, periodSec }, {
        sma20: hints.sma20, sma50: hints.sma50, rsi14: hints.rsi14,
        trend: hints.trend, support: hints.support, resistance: hints.resistance,
      });
      artifacts.push({ kind: 'hints', payload: hints });
      answer = `${inst.name} (${inst.symbol}), период ${periodLabel(periodSec)}: последняя цена ${inst.last} ₽ (${inst.changePct > 0 ? '+' : ''}${inst.changePct}% за день). ` +
        `SMA20 ${hints.sma20} / SMA50 ${hints.sma50}, RSI14 ${hints.rsi14}, ATR14 ${hints.atr14}. ` +
        `Тренд: ${hints.trend === 'up' ? 'восходящий' : hints.trend === 'down' ? 'нисходящий' : 'боковой'}; поддержка ${hints.support} ₽, сопротивление ${hints.resistance} ₽. ` +
        'Могу запустить бэктест стратегий или автоторговлю на бумажном счёте — скажите, что интересует.';
    }
    return {
      answer,
      toolCalls,
      artifacts,
      llm: false,
      elapsedMs: Math.round(performance.now() - startedAt),
    };
  }

  autonomyStatus() {
    return JSON.parse(JSON.stringify(this.autonomyState));
  }

  async agentStatus() {
    await this.ready;
    return {
      autonomy: this.autonomyStatus(),
      llm: { enabled: false, model: '', note: 'локальный аналитик (мок-данные)' },
    };
  }

  syncAutonomy() {
    this.autonomyState.log = this.autoLog;
    this.autonomyState.lastActionAt = nowIso();
  }

  startAutonomy(config) {
    if (this.autonomyState && this.autonomyState.running) throw mockError('conflict', 'Автономия уже запущена');
    const inst = this.bySymbol.get(config.symbol) || this.bySymbol.get('TQBR:SBER');
    const cfg = {
      symbol: inst.symbol,
      periodSec: Number(config.periodSec) || 300,
      strategy: config.strategy || 'sma_cross',
      params: Object.assign({ fast: 10, slow: 30 }, config.params || {}),
      speed: Math.max(1, Math.min(60, Number(config.speed) || 5)),
      budget: Math.max(10000, Number(config.budget) || 200000),
      maxPositionPct: Number(config.maxPositionPct) || 25,
      stopLossPct: Number(config.stopLossPct) || 2,
      takeProfitPct: Number(config.takeProfitPct) || 5,
      dailyLossPct: Number(config.dailyLossPct) || 3,
      live: false,
    };
    this.autonomyStep = 0;
    this.autonomyState = {
      running: true,
      config: cfg,
      startedAt: nowIso(),
      lastActionAt: nowIso(),
      trades: 0,
      pnl: 0,
      pnlPct: 0,
      log: [{
        time: nowIso(),
        kind: 'start',
        message: `Автономия запущена: ${inst.seccode}, ${periodLabel(cfg.periodSec)}, «${strategyTitle(cfg.strategy)}», бюджет ${cfg.budget.toLocaleString('ru-RU')} ₽, скорость ${cfg.speed} бар/с`,
        data: {},
      }],
    };
    this.autoLog = this.autonomyState.log;
    clearInterval(this.autonomyTimer);
    this.autonomyTimer = setInterval(() => this.autonomyTick(), Math.max(320, Math.round(1200 / cfg.speed) + 300));
    this.emitMsg({ type: 'agent', data: { type: 'autonomy', data: this.autonomyStatus() } });
    return this.autonomyStatus();
  }

  autonomyTick() {
    if (!this.autonomyState || !this.autonomyState.running) return;
    this.autonomyStep += 1;
    const cfg = this.autonomyState.config;
    const inst = this.bySymbol.get(cfg.symbol);
    const step = this.autonomyStep;
    const kinds = [
      ['signal', `Сигнал ${strategyTitle(cfg.strategy)}: вход в лонг по ${(inst.last - inst.minStep).toFixed(inst.decimals)} ₽`],
      ['order', `Заявка: покупка ${Math.round(cfg.budget / inst.last)} ${inst.seccode} по рынку (бюджет ${cfg.budget.toLocaleString('ru-RU')} ₽)`],
      ['fill', `Исполнено: покупка ${Math.round(cfg.budget / inst.last)} ${inst.seccode} по ${inst.last} ₽, комиссия ${(cfg.budget * 0.0005).toFixed(2)} ₽`],
      ['signal', 'Сигнал на выход: цена развернулась против позиции'],
      ['fill', `Исполнено: продажа по ${(inst.last + inst.minStep).toFixed(inst.decimals)} ₽, результат положительный`],
      ['risk_block', `Риск-лимит: превышен дневной лимит убытка ${cfg.dailyLossPct}%, новые заявки блокируются`],
    ];
    const [kind, message] = kinds[(step - 1) % kinds.length];
    const delta = kind === 'fill' ? roundTo((Math.random() - 0.35) * 420, 2) : 0;
    this.autonomyState.trades += kind === 'fill' ? 1 : 0;
    this.autonomyState.pnl = roundTo(this.autonomyState.pnl + delta, 2);
    this.autonomyState.pnlPct = roundTo(this.autonomyState.pnl / Math.max(1, cfg.budget) * 100, 2);
    this.autonomyState.log.push({ time: nowIso(), kind, message, data: {} });
    if (this.autonomyState.log.length > 80) this.autonomyState.log = this.autonomyState.log.slice(-80);
    this.autoLog = this.autonomyState.log;
    this.autonomyState.lastActionAt = nowIso();
    this.emitMsg({ type: 'agent', data: { type: 'log', data: this.autonomyState.log[this.autonomyState.log.length - 1] } });
    this.emitMsg({ type: 'agent', data: { type: 'autonomy', data: this.autonomyStatus() } });
    if (step >= 12) {
      this.autonomyState.log.push({
        time: nowIso(),
        kind: 'stop',
        message: `Replay завершён: ${this.autonomyState.trades} сделок, итог ${this.autonomyState.pnl >= 0 ? '+' : ''}${this.autonomyState.pnl.toFixed(2)} ₽ (${this.autonomyState.pnlPct}%)`,
        data: {},
      });
      this.autoLog = this.autonomyState.log;
      this.stopAutonomy(true);
    }
  }

  stopAutonomy(silent = false) {
    clearInterval(this.autonomyTimer);
    this.autonomyTimer = 0;
    if (this.autonomyState) {
      this.autonomyState.running = false;
      if (!silent) {
        this.autonomyState.log.push({
          time: nowIso(),
          kind: 'stop',
          message: `Автономия остановлена вручную: итог ${this.autonomyState.pnl >= 0 ? '+' : ''}${this.autonomyState.pnl.toFixed(2)} ₽ (${this.autonomyState.pnlPct}%)`,
          data: {},
        });
        this.autoLog = this.autonomyState.log;
      }
      this.emitMsg({ type: 'agent', data: { type: 'autonomy', data: this.autonomyStatus() } });
    }
    return this.autonomyStatus();
  }

  async autonomy(payload) {
    await this.ready;
    const action = payload && payload.action;
    if (action === 'start') {
      if (!payload.config) throw mockError('bad_request', 'Не передан конфиг автономии');
      return this.startAutonomy(payload.config);
    }
    if (action === 'stop') return this.stopAutonomy();
    throw mockError('bad_request', 'Неизвестное действие автономии');
  }

  async autonomyLog(limit = 200) {
    await this.ready;
    return (this.autoLog || []).slice(-limit);
  }

  async agentBacktests() {
    await this.ready;
    return [...this.extraBacktests, ...this.data.backtests.results];
  }

  // ---- emulated SSE ----------------------------------------------------

  onEvent(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  emitMsg(message) {
    for (const listener of this.listeners) {
      try {
        listener(message);
      } catch (error) {
        console.error('[mock sse] listener failed', error);
      }
    }
  }

  connectEvents() {
    this.closed = false;
    this.emitMsg({ type: '_sse', data: { state: 'open' } });
    this.emitMsg({ type: 'status', data: this.statusPayload() });
    clearInterval(this.tickTimer);
    clearInterval(this.slowTimer);
    this.tickTimer = setInterval(() => this.marketTick(), 1000);
    this.slowTimer = setInterval(() => {
      this.emitMsg({ type: 'status', data: this.statusPayload() });
      this.emitMsg({ type: 'portfolio', data: this.portfolioData });
    }, 5000);
  }

  marketTick() {
    const rnd = Math.random;
    const sample = new Set();
    if (this.bySymbol.has('TQBR:SBER')) sample.add('TQBR:SBER');
    const symbols = this.instrumentsData.map((inst) => inst.symbol);
    while (sample.size < 4) sample.add(symbols[Math.floor(rnd() * symbols.length)]);
    for (const symbol of sample) {
      const inst = this.bySymbol.get(symbol);
      const step = inst.minStep || 0.01;
      const direction = rnd() > 0.5 ? 1 : -1;
      const move = direction * step * Math.round(1 + rnd() * 2);
      inst.last = roundTo(Math.max(step, inst.last + move), inst.decimals);
      inst.bid = roundTo(inst.last - step, inst.decimals);
      inst.ask = roundTo(inst.last + step, inst.decimals);
      inst.change = roundTo(inst.last - inst.prevClose, inst.decimals);
      inst.changePct = roundTo((inst.change / inst.prevClose) * 100, 2);
      inst.volume += Math.round(rnd() * 25000 * (inst.lotSize || 1));
      inst.updatedAt = nowIso();
      if (Array.isArray(inst.spark) && inst.spark.length) {
        inst.spark.push(inst.last);
        if (inst.spark.length > 60) inst.spark.shift();
      }
      this.emitMsg({
        type: 'quote',
        data: {
          symbol: inst.symbol,
          last: inst.last,
          change: inst.change,
          changePct: inst.changePct,
          bid: inst.bid,
          ask: inst.ask,
          volume: inst.volume,
          time: nowIso(),
        },
      });
    }
    this.recomputePortfolio();
  }

  disconnect() {
    this.closed = true;
    clearInterval(this.tickTimer);
    clearInterval(this.slowTimer);
    clearInterval(this.autonomyTimer);
    this.emitMsg({ type: '_sse', data: { state: 'closed' } });
  }
}
