// utils.js — formatting, DOM and small data helpers shared by every panel.
// All user-facing numbers/times are Russian and shown in Moscow exchange time.

export const MSK_TZ = 'Europe/Moscow';

const nfCache = new Map();

function nf(min, max) {
  const key = `${min}:${max}`;
  let f = nfCache.get(key);
  if (!f) {
    f = new Intl.NumberFormat('ru-RU', {
      minimumFractionDigits: min,
      maximumFractionDigits: max,
      useGrouping: true,
    });
    nfCache.set(key, f);
  }
  return f;
}

const isNum = (v) => v !== null && v !== undefined && v !== '' && Number.isFinite(Number(v));

/** 1234.5 → "1 234,50" (ru-RU grouping + comma decimal). */
export function fmtNum(value, decimals = 2) {
  if (!isNum(value)) return '—';
  return nf(decimals, decimals).format(Number(value));
}

/** Price with the instrument's decimals, no currency sign. */
export function fmtPrice(value, decimals = 2) {
  return fmtNum(value, decimals);
}

/** Price with the rouble sign: "293,22 ₽". */
export function fmtRub(value, decimals = 2) {
  if (!isNum(value)) return '—';
  return `${fmtNum(value, decimals)} ₽`;
}

/** Signed money value: "+44,90 ₽" / "-6,40 ₽" / "0,00 ₽". */
export function fmtSignedRub(value, decimals = 2) {
  if (!isNum(value)) return '—';
  const n = Number(value);
  const text = fmtNum(Math.abs(n), decimals);
  const sign = n > 0 ? '+' : n < 0 ? '-' : '';
  return `${sign}${text} ₽`;
}

/** Signed number, e.g. change per share: "+0,0248". */
export function fmtSigned(value, decimals = 2) {
  if (!isNum(value)) return '—';
  const n = Number(value);
  const text = fmtNum(Math.abs(n), decimals);
  const sign = n > 0 ? '+' : n < 0 ? '-' : '';
  return `${sign}${text}`;
}

/** Percent without sign: "17,10%". */
export function fmtPct(value, decimals = 2) {
  if (!isNum(value)) return '—';
  return `${fmtNum(value, decimals)}%`;
}

/** Percent with sign: "+17,10%". */
export function fmtSignedPct(value, decimals = 2) {
  if (!isNum(value)) return '—';
  const n = Number(value);
  return `${n > 0 ? '+' : n < 0 ? '-' : ''}${fmtNum(Math.abs(n), decimals)}%`;
}

export function fmtQty(value) {
  if (!isNum(value)) return '—';
  return nf(0, 0).format(Number(value));
}

/** 12345678 → "12,3 млн"; 456000 → "456 тыс." */
export function fmtVolume(value) {
  if (!isNum(value)) return '—';
  const n = Number(value);
  const abs = Math.abs(n);
  if (abs >= 1e9) return `${fmtNum(n / 1e9, 2)} млрд`;
  if (abs >= 1e6) return `${fmtNum(n / 1e6, 2)} млн`;
  if (abs >= 1e3) return `${nf(0, 1).format(n / 1e3)} тыс.`;
  return fmtNum(n, 0);
}

const timeFmt = new Intl.DateTimeFormat('ru-RU', {
  timeZone: MSK_TZ, hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
});
const hmFmt = new Intl.DateTimeFormat('ru-RU', {
  timeZone: MSK_TZ, hour: '2-digit', minute: '2-digit', hour12: false,
});
const dmFmt = new Intl.DateTimeFormat('ru-RU', {
  timeZone: MSK_TZ, day: '2-digit', month: '2-digit',
});
const fullFmt = new Intl.DateTimeFormat('ru-RU', {
  timeZone: MSK_TZ, day: '2-digit', month: '2-digit', year: 'numeric',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
});
const dateFmt = new Intl.DateTimeFormat('ru-RU', {
  timeZone: MSK_TZ, day: '2-digit', month: '2-digit', year: 'numeric',
});

function toDate(value) {
  if (value instanceof Date) return value;
  if (typeof value === 'number') return new Date(value < 1e12 ? value * 1000 : value);
  if (typeof value === 'string' && value) return new Date(value);
  return null;
}

/** "21:02:14" in Moscow time. */
export function fmtClock(value = new Date()) {
  const d = toDate(value);
  return d ? timeFmt.format(d) : '—';
}

/** "21:02" in Moscow time. */
export function fmtHM(value) {
  const d = toDate(value);
  return d ? hmFmt.format(d) : '—';
}

/** "05.11". */
export function fmtDayMonth(value) {
  const d = toDate(value);
  return d ? dmFmt.format(d) : '—';
}

/** "05.11.2026". */
export function fmtDate(value) {
  const d = toDate(value);
  return d ? dateFmt.format(d) : '—';
}

/** "05.11.2026, 21:02:14". */
export function fmtDateTime(value) {
  const d = toDate(value);
  return d ? fullFmt.format(d) : '—';
}

/** "только что" / "2 мин назад" / "14:05". */
export function isValidTime(value) {
  const date = toDate(value);
  return !!date && date.getFullYear() >= 2020;
}

export function timeAgo(value) {
  const d = toDate(value);
  if (!d) return '—';
  const diff = Math.max(0, Date.now() - d.getTime());
  if (diff < 30e3) return 'только что';
  if (diff < 60e3) return `${Math.floor(diff / 1000)} сек назад`;
  if (diff < 3600e3) return `${Math.floor(diff / 60e3)} мин назад`;
  if (diff < 6 * 3600e3) return `${Math.floor(diff / 3600e3)} ч назад`;
  return fmtHM(d);
}

export function signClass(value) {
  if (!isNum(value)) return 'flat';
  const n = Number(value);
  if (n > 0) return 'up';
  if (n < 0) return 'down';
  return 'flat';
}

export function arrow(value) {
  if (!isNum(value)) return '';
  const n = Number(value);
  return n > 0 ? '▲' : n < 0 ? '▼' : '—';
}

export function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

export function debounce(fn, ms = 160) {
  let timer = 0;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}

/** Escape untrusted text before inserting into innerHTML. */
export function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[ch]));
}

export function qs(sel, root = document) {
  return root.querySelector(sel);
}

export function qsa(sel, root = document) {
  return Array.from(root.querySelectorAll(sel));
}

/** Delegated event listener: on(container, 'click', '[data-action]', handler). */
export function on(root, type, selector, handler) {
  root.addEventListener(type, (event) => {
    const target = event.target.closest(selector);
    if (target && root.contains(target)) handler(event, target);
  });
}

export function uid(prefix = 'id') {
  return `${prefix}_${Math.random().toString(36).slice(2, 9)}`;
}

/** "TQBR:SBER" → {board:'TQBR', seccode:'SBER'}. */
export function parseSymbol(symbol) {
  const text = String(symbol || '');
  const i = text.indexOf(':');
  if (i < 0) return { board: '', seccode: text };
  return { board: text.slice(0, i), seccode: text.slice(i + 1) };
}

export function pluralRu(n, forms) {
  const abs = Math.abs(Number(n) || 0) % 100;
  const last = abs % 10;
  if (abs > 10 && abs < 20) return forms[2];
  if (last > 1 && last < 5) return forms[1];
  if (last === 1) return forms[0];
  return forms[2];
}

export const PERIODS = [
  { sec: 60, label: '1м' },
  { sec: 300, label: '5м' },
  { sec: 900, label: '15м' },
  { sec: 3600, label: '1ч' },
  { sec: 86400, label: '1д' },
];

export function periodLabel(sec) {
  const found = PERIODS.find((p) => p.sec === Number(sec));
  return found ? found.label : `${Math.round(Number(sec) / 60)}м`;
}

export const STRATEGY_TITLES = {
  sma_cross: 'SMA-кросс',
  rsi_reversion: 'RSI-возврат',
  donchian_breakout: 'Пробой Дончиана',
  momentum: 'Моментум',
};

export function strategyTitle(id, fallback = '') {
  return STRATEGY_TITLES[id] || fallback || id || '—';
}

export const SIDE_TITLES = { buy: 'Покупка', sell: 'Продажа' };
export const STATUS_TITLES = {
  new: 'Активна', filled: 'Исполнена', partial: 'Частично',
  cancelled: 'Отменена', rejected: 'Отклонена',
};
export const SOURCE_TITLES = {
  ui: 'терминал', agent: 'AI-агент', autonomy: 'автономия', manual: 'вручную',
};
export const LOG_KIND_TITLES = {
  signal: 'сигнал', order: 'заявка', fill: 'исполнение', risk_block: 'риск-блок',
  start: 'старт', stop: 'стоп', error: 'ошибка', info: 'инфо',
};

/** Round a price to the instrument tick. */
export function roundToStep(price, step) {
  if (!step || !Number.isFinite(Number(price))) return Number(price) || 0;
  return Math.round(Number(price) / Number(step)) * Number(step);
}

/** Nice axis step: 1/2/2.5/5 × 10^k for a target tick count. */
export function niceStep(range, targetCount = 6) {
  const raw = Math.abs(range) / Math.max(1, targetCount);
  if (!Number.isFinite(raw) || raw <= 0) return 1;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const norm = raw / mag;
  const nice = norm < 1.5 ? 1 : norm < 3 ? 2 : norm < 7 ? 2.5 : norm < 15 ? 5 : 10;
  return nice * mag;
}

/** Same-quote sparkline colouring: green if last bar closes above the first. */
export function sparkPositive(values) {
  if (!Array.isArray(values) || values.length < 2) return true;
  return values[values.length - 1] >= values[0];
}
