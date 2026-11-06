// app.js — application bootstrap, layout rendering and interactions.
// The whole UI is buildless: plain ES modules, canvas charts, no external assets.

import { createApi, createInitialState, Store } from './api.js';
import { AIDrawer } from './ai.js';
import { CandleChart, EquityChart, drawSparkline } from './chart.js';
import { icon, logoSvg, sparkSvg } from './icons.js';
import {
  LOG_KIND_TITLES, PERIODS, SIDE_TITLES, SOURCE_TITLES, STATUS_TITLES,
  arrow, clamp, debounce, esc, fmtClock, fmtDate, fmtDateTime, fmtDayMonth,
  fmtHM, fmtNum, fmtPct, fmtPrice, fmtQty, fmtRub, fmtSignedPct, fmtSignedRub,
  fmtVolume, on, parseSymbol, periodLabel, pluralRu, signClass, strategyTitle,
} from './utils.js';

const store = new Store(createInitialState());

const refs = {};
const rowRefs = new Map();
let api = null;
let chart = null;
let ai = null;
let chartType = 'candles';
let serverOffset = 0;
let sseState = 'idle';
let selectedSymbol = null;
let analyticsCharts = [];
let watchFilterTimer = 0;
let statusPollTimer = 0;
let quotePollTimer = 0;
let loadToken = 0;

const RAIL_SECTIONS = [
  { id: 'favorites', title: 'Избранное', icon: 'star', action: 'rail-favorites' },
  { id: 'portfolio', title: 'Портфель', icon: 'wallet', action: 'rail-portfolio' },
  { id: 'terminal', title: 'Терминал', icon: 'candles', action: 'rail-terminal' },
  { id: 'ideas', title: 'Идеи', icon: 'bulb', soon: true },
  { id: 'calendar', title: 'Календарь', icon: 'calendar', soon: true },
  { id: 'notifications', title: 'Уведомления', icon: 'bell', soon: true },
  { id: 'mail', title: 'Почта', icon: 'mail', soon: true },
  { id: 'market', title: 'Рынки', icon: 'globe', soon: true },
  { id: 'chat', title: 'AI-агент', icon: 'chat', action: 'rail-chat' },
  { id: 'learning', title: 'Обучение', icon: 'graduation', soon: true },
  { id: 'help', title: 'Помощь', icon: 'help', soon: true },
];

const BADGE_PALETTE = ['#2d6cdf', '#0f9d6b', '#e5484d', '#7b61ff', '#c98a00',
  '#0d8fb3', '#d94f8a', '#4f7a21', '#8a5cff', '#ad6200', '#5a7d8c', '#b0433d'];

const ticket = { side: 'buy', type: 'market', qty: 1, price: '', touched: false };
const favorites = new Set(['TQBR:SBER']);

// ---------------------------------------------------------------------------
// bootstrap
// ---------------------------------------------------------------------------

function boot() {
  refs.app = document.getElementById('app');
  refs.rail = document.getElementById('rail');
  refs.watchlistHead = document.getElementById('watchlistHead');
  refs.watchlistList = document.getElementById('watchlistList');
  refs.instHeader = document.getElementById('instHeader');
  refs.chartToolbar = document.getElementById('chartToolbar');
  refs.chartOhlc = document.getElementById('chartOhlc');
  refs.chartCanvas = document.getElementById('chartCanvas');
  refs.chartTooltip = document.getElementById('chartTooltip');
  refs.chartLoading = document.getElementById('chartLoading');
  refs.chartEmpty = document.getElementById('chartEmpty');
  refs.chartOverlayBadge = document.getElementById('chartOverlayBadge');
  refs.rightTabs = document.getElementById('rightTabs');
  refs.bottomPanel = document.getElementById('bottomPanel');
  refs.bottomSummary = document.getElementById('bottomSummary');
  refs.bottomTabs = document.getElementById('bottomTabs');
  refs.bottomBody = document.getElementById('bottomBody');
  refs.statusLeft = document.getElementById('statusLeft');
  refs.statusRight = document.getElementById('statusRight');
  refs.toasts = document.getElementById('toasts');
  refs.drawer = document.getElementById('aiDrawer');
  refs.drawerBackdrop = document.getElementById('drawerBackdrop');
  document.body.dataset.mock = new URLSearchParams(window.location.search).get('mock') === '1' ? '1' : '0';

  injectHeaderIcons();
  renderRail();
  chart = new CandleChart(refs.chartCanvas, refs.chartTooltip);
  chart.onSelectionChange = (selection) => {
    store.set({ chartSelection: selection });
    renderToolbar();
  };
  renderSkeleton();
  bindEvents();
  setStatusClock();
  setInterval(setStatusClock, 1000);
  startApp();
}

function injectHeaderIcons() {
  document.getElementById('brandMark').innerHTML = logoSvg(28);
  document.getElementById('searchIcon').innerHTML = icon('search', 17);
  document.getElementById('aiBtnIcon').innerHTML = sparkSvg(15);
  document.getElementById('notifIcon').innerHTML = icon('bell', 18);
  document.getElementById('helpIcon').innerHTML = icon('help', 18);
}

async function startApp() {
  try {
    api = await createApi({ store });
    const health = await api.health();
    applyHealth(health);
    const [instrumentsPayload, portfolio, orders, trades, strategies, agentStatus, backtests, proposals] = await Promise.all([
      api.instruments(),
      api.portfolio(),
      api.orders(),
      api.tradeHistory(),
      api.strategies().catch(() => []),
      api.agentStatus().catch(() => null),
      api.agentBacktests().catch(() => []),
      api.agentProposals().catch(() => []),
    ]);
    const instruments = instrumentsPayload.instruments || [];
    store.set({
      ready: true,
      loading: false,
      instruments,
      portfolio,
      orders,
      tradeHistory: trades,
      strategies,
      agentStatus,
      backtests,
      proposals,
      autonomy: agentStatus && agentStatus.autonomy ? agentStatus.autonomy : null,
      autonomyLog: agentStatus && agentStatus.autonomy && agentStatus.autonomy.log ? agentStatus.autonomy.log : [],
      llm: health.llm || (agentStatus && agentStatus.llm) || null,
      serverTime: instrumentsPayload.serverTime || health.serverTime || new Date().toISOString(),
    });
    selectedSymbol = instruments.some((inst) => inst.symbol === 'TQBR:SBER')
      ? 'TQBR:SBER' : (instruments[0] ? instruments[0].symbol : null);
    store.set({ selected: selectedSymbol });
    resetTicket();
    ai = new AIDrawer({
      root: refs.drawer,
      backdrop: refs.drawerBackdrop,
      store,
      api,
      chart,
      onToast: toast,
      onOpenBacktests: () => switchBottomTab('analytics'),
      getScreenContext: buildScreenContext,
    });
    renderAll();
    if (selectedSymbol) await loadSymbol(selectedSymbol, store.get().periodSec);
    api.onEvent(handleServerEvent);
    api.connectEvents();
    startPolling();
    updateConnection();
    applyDeepLinks();
  } catch (error) {
    showFatal(error);
  }
}

function applyHealth(health) {
  if (!health) return;
  store.set({
    health,
    mode: health.mode || 'offline',
    connected: !!health.connected,
    serverTime: health.serverTime || new Date().toISOString(),
    exchangeTZ: health.exchangeTZ || 'Europe/Moscow',
    llm: health.llm || null,
  });
  if (health.serverTime) {
    const parsed = Date.parse(health.serverTime);
    if (Number.isFinite(parsed)) serverOffset = Date.now() - parsed;
  }
  renderHeader();
  updateConnection();
}

function startPolling() {
  clearInterval(statusPollTimer);
  clearInterval(quotePollTimer);
  statusPollTimer = setInterval(async () => {
    if (document.hidden) return;
    try {
      const health = await api.health();
      applyHealth(health);
    } catch { /* SSE keeps its own state; ignore polling errors */ }
    if (api.isMock) return;
    try {
      const [portfolio, orders, tradeHistory] = await Promise.all([
        api.portfolio(), api.orders(), api.tradeHistory(),
      ]);
      store.set({ portfolio, orders, tradeHistory });
      renderBottomSummary();
      renderBottomBody();
    } catch { /* ignore */ }
    try {
      const status = await api.agentStatus();
      if (status && status.autonomy) {
        store.set({ autonomy: status.autonomy, autonomyLog: status.autonomy.log || [] });
      }
      if (status && status.llm) store.set({ llm: status.llm });
    } catch { /* ignore */ }
  }, api.isMock ? 60000 : 12000);
  quotePollTimer = setInterval(async () => {
    if (document.hidden) return;
    const state = store.get();
    if (!state.selected || state.candlesLoading) return;
    if (state.ui.rightTab === 'orderbook' || state.ui.rightTab === 'trades') {
      try {
        const orderbook = await api.orderbook(state.selected);
        store.set({ orderbook });
        if (state.ui.rightTab === 'orderbook') renderRightTabs();
      } catch { /* ignore */ }
    }
    if (state.ui.rightTab === 'trades') loadTrades();
  }, 3500);
}

// ---------------------------------------------------------------------------
// error / skeleton states
// ---------------------------------------------------------------------------

function renderSkeleton() {
  refs.watchlistHead.innerHTML = `
    <div class="wl-account skeleton-row"><span class="skeleton" style="width:160px;height:16px"></span></div>
    <div class="wl-filter"><span class="skeleton" style="width:100%;height:30px;border-radius:8px"></span></div>`;
  refs.watchlistList.innerHTML = Array.from({ length: 9 }).map(() => `
    <div class="wl-row skeleton-row">
      <span class="skeleton" style="width:32px;height:32px;border-radius:50%"></span>
      <span class="skeleton-col"><span class="skeleton" style="width:70%;height:11px"></span><span class="skeleton" style="width:45%;height:9px"></span></span>
      <span class="skeleton" style="width:60px;height:22px"></span>
    </div>`).join('');
  refs.instHeader.innerHTML = `<div class="inst-head skeleton-row">
    <span class="skeleton" style="width:32px;height:32px;border-radius:8px"></span>
    <span class="skeleton-col"><span class="skeleton" style="width:180px;height:16px"></span><span class="skeleton" style="width:120px;height:10px"></span></span>
    <span class="skeleton" style="width:120px;height:26px;margin-left:auto"></span>
  </div>`;
  refs.chartToolbar.innerHTML = `<div class="skeleton" style="width:260px;height:30px"></div>`;
  refs.chartOhlc.innerHTML = '';
  refs.chartLoading.hidden = false;
  refs.rightTabs.innerHTML = `<div class="right-tabs-skeleton"><span class="skeleton" style="width:100%;height:34px"></span>${Array.from({ length: 8 }).map(() => '<span class="skeleton" style="width:100%;height:22px;margin-top:6px"></span>').join('')}</div>`;
  refs.bottomSummary.innerHTML = `<div class="bs-skeleton skeleton-row">
    <span class="skeleton" style="width:220px;height:14px"></span>
    <span class="skeleton" style="width:160px;height:22px;margin-left:auto"></span>
    <span class="skeleton" style="width:160px;height:22px"></span>
    <span class="skeleton" style="width:160px;height:22px"></span>
  </div>`;
  refs.bottomTabs.innerHTML = '';
  refs.bottomBody.innerHTML = `<div class="bottom-skeleton">${Array.from({ length: 6 }).map(() => '<span class="skeleton" style="width:100%;height:24px;margin-top:4px"></span>').join('')}</div>`;
  refs.statusLeft.textContent = 'AI Terminal · загрузка…';
}

function showFatal(error) {
  const message = (error && error.message) || 'Не удалось загрузить данные терминала';
  const isNetwork = error && (error.code === 'network' || error.code === 'timeout');
  refs.app.innerHTML = `
    <div class="fatal">
      <div class="fatal-card">
        <div class="fatal-icon">${icon('warning', 30)}</div>
        <h1>Нет соединения с терминалом</h1>
        <p>${esc(message)}</p>
        ${isNetwork ? `<p class="muted">Бэкенд не отвечает. Запустите <code>go run ./cmd/terminal -mode offline</code> и откройте
          <code>http://127.0.0.1:8090</code>. Для проверки вёрстки без бэкенда добавьте в адрес <code>?mock=1</code>.</p>` : ''}
        <button type="button" class="btn btn-ai" data-action="retry">${icon('refresh', 16)} Повторить</button>
      </div>
    </div>`;
  refs.app.addEventListener('click', (event) => {
    if (event.target.closest('[data-action="retry"]')) window.location.reload();
  });
}

// ---------------------------------------------------------------------------
// header / rail / status bar
// ---------------------------------------------------------------------------

function renderHeader() {
  const state = store.get();
  const mode = document.getElementById('modeBadge');
  const isMock = document.body.dataset.mock === '1';
  let label = 'ДЕМО';
  let title = 'Офлайн-снапшот (демо-данные)';
  if (isMock) {
    label = 'ДЕМО';
    title = 'Демо-режим: мок-данные без бэкенда (?mock=1)';
  } else if (state.mode === 'live') {
    label = 'LIVE';
    title = 'Живые данные биржи, торговля только бумажная';
  }
  mode.textContent = label;
  mode.title = title;
  mode.className = `mode-badge mode-${label.toLowerCase()}`;
  const aiButton = document.getElementById('aiBtn');
  aiButton.classList.toggle('active', state.ui.drawerOpen);
}

function renderRail() {
  const active = store.get().ui.page;
  refs.rail.innerHTML = RAIL_SECTIONS.map((section) => {
    const isActive = section.id === active;
    const title = section.soon ? `${section.title} — скоро` : section.title;
    return `<button type="button" class="rail-btn ${isActive ? 'active' : ''} ${section.soon ? 'soon' : ''}"
      data-rail="${section.id}" title="${esc(title)}" aria-label="${esc(title)}"
      ${isActive ? 'aria-current="page"' : ''}>${icon(section.icon, 20)}</button>`;
  }).join('');
}

function setStatusClock() {
  const msk = fmtClock(new Date(Date.now() - serverOffset));
  const clockEl = document.querySelector('#srvTime .srv-clock');
  if (clockEl) clockEl.textContent = msk;
  const local = new Intl.DateTimeFormat('ru-RU', {
    day: '2-digit', month: '2-digit', year: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(new Date());
  if (refs.statusRight) refs.statusRight.textContent = `Местное время ${local}`;
  const state = store.get();
  if (refs.statusLeft) {
    const version = (state.health && state.health.version) || '0.1.0';
    const source = state.mode === 'live' ? 'live данные' : state.mode === 'offline' ? 'офлайн-снапшот' : 'демо';
    refs.statusLeft.textContent = `AI Terminal v${version} · ${source} · бумажный счёт`;
  }
}

function updateConnection() {
  const state = store.get();
  const el = document.getElementById('connState');
  if (!el) return;
  const text = el.querySelector('.conn-text');
  const connected = state.connected;
  let label = 'нет связи';
  let cls = 'offline';
  if (api && api.isMock) {
    label = 'локальные данные';
    cls = 'online';
  } else if (sseState === 'open' && connected) {
    label = 'онлайн';
    cls = 'online';
  } else if (sseState === 'error') {
    label = 'переподключение…';
    cls = 'warn';
  } else if (connected) {
    label = 'подключено';
    cls = 'online';
  }
  el.className = `conn-state ${cls}`;
  if (text) text.textContent = label;
  el.title = `Соединение: ${label}${state.health && state.health.message ? ` · ${state.health.message}` : ''}`;
}

// ---------------------------------------------------------------------------
// watchlist
// ---------------------------------------------------------------------------

function renderWatchlistHead() {
  const state = store.get();
  const cash = state.portfolio ? state.portfolio.cash : null;
  refs.watchlistHead.innerHTML = `
    <div class="wl-account">
      <button type="button" class="account-btn" data-action="account-menu" title="Демо-счёт бумажной торговли">
        <span class="account-dot"></span>
        <span class="account-text">
          <span class="account-name">ДЕМО-счёт</span>
          <span class="account-cash muted" id="wlAccountCash">свободно ${cash === null ? '—' : fmtRub(cash)}</span>
        </span>
        ${icon('chevronDown', 14)}
      </button>
      <div class="wl-head-actions">
        <button type="button" class="icon-btn small" data-action="refresh-data" title="Обновить данные" aria-label="Обновить данные">${icon('refresh', 15)}</button>
        <button type="button" class="icon-btn small" data-action="focus-search" title="Поиск (/)" aria-label="Поиск">${icon('search', 15)}</button>
      </div>
    </div>
    <div class="wl-filter">
      ${icon('search', 14)}
      <input id="watchFilter" type="search" placeholder="Фильтр: тикер или название" autocomplete="off"
        value="${esc(state.ui.watchFilter)}" aria-label="Фильтр инструментов">
    </div>`;
}

function renderWatchlist() {
  const state = store.get();
  const filter = state.ui.watchFilter.trim().toLowerCase();
  const instruments = state.instruments.filter((inst) => {
    if (!filter) return true;
    return inst.symbol.toLowerCase().includes(filter)
      || (inst.name || '').toLowerCase().includes(filter)
      || (inst.seccode || '').toLowerCase().includes(filter);
  });
  rowRefs.clear();
  if (!instruments.length) {
    refs.watchlistList.innerHTML = `<div class="empty-state small">${icon('search', 22)}<p>Ничего не найдено</p></div>`;
    return;
  }
  refs.watchlistList.innerHTML = instruments.map((inst) => {
    const changeCls = signClass(inst.change);
    const isActive = inst.symbol === state.selected;
    return `
      <button type="button" class="wl-row ${isActive ? 'active' : ''}" data-symbol="${esc(inst.symbol)}"
        role="option" aria-selected="${isActive ? 'true' : 'false'}">
        <span class="wl-badge" style="--badge:${badgeColor(inst.symbol)}">${esc(badgeLetters(inst))}</span>
        <span class="wl-main">
          <span class="wl-name">${esc(inst.shortName || inst.name || inst.seccode)}</span>
          <span class="wl-meta">${esc(inst.board || 'MOEX')} · ${fmtHM(inst.updatedAt)}${favorites.has(inst.symbol) ? ' · ★' : ''}</span>
        </span>
        <span class="wl-spark"><canvas width="76" height="26" aria-hidden="true"></canvas></span>
        <span class="wl-price">
          <b data-role="wl-price">${fmtRub(inst.last, inst.decimals)}</b>
          <span class="chg ${changeCls}" data-role="wl-change">${fmtSignedRub(inst.change, inst.decimals)} <span class="chg-pct">(${fmtSignedPct(inst.changePct)})</span></span>
        </span>
      </button>`;
  }).join('');
  const bySymbol = new Map(instruments.map((inst) => [inst.symbol, inst]));
  refs.watchlistList.querySelectorAll('.wl-row[data-symbol]').forEach((row) => {
    const inst = bySymbol.get(row.dataset.symbol);
    if (!inst) return;
    const canvas = row.querySelector('canvas');
    drawSparkline(canvas, inst.spark, { positive: inst.change >= 0 });
    rowRefs.set(inst.symbol, { row, spark: canvas });
  });
}

function updateWatchRow(inst) {
  const entry = rowRefs.get(inst.symbol);
  if (!entry) return;
  const priceEl = entry.row.querySelector('[data-role="wl-price"]');
  const changeEl = entry.row.querySelector('[data-role="wl-change"]');
  if (priceEl) priceEl.textContent = fmtRub(inst.last, inst.decimals);
  if (changeEl) {
    changeEl.className = `chg ${signClass(inst.change)}`;
    changeEl.innerHTML = `${fmtSignedRub(inst.change, inst.decimals)} <span class="chg-pct">(${fmtSignedPct(inst.changePct)})</span>`;
  }
  drawSparkline(entry.spark, inst.spark, { positive: inst.change >= 0 });
}

function badgeColor(symbol) {
  let hash = 0;
  for (let i = 0; i < symbol.length; i += 1) hash = (hash * 31 + symbol.charCodeAt(i)) >>> 0;
  return BADGE_PALETTE[hash % BADGE_PALETTE.length];
}

function badgeLetters(inst) {
  const seccode = inst.seccode || parseSymbol(inst.symbol).seccode || '?';
  return seccode.slice(0, 2).toUpperCase();
}

// ---------------------------------------------------------------------------
// center: instrument header, toolbar, chart, right tabs
// ---------------------------------------------------------------------------

function currentInstrument() {
  const state = store.get();
  return state.instruments.find((inst) => inst.symbol === state.selected) || null;
}

function renderInstrumentHeader() {
  const inst = currentInstrument();
  if (!inst) {
    refs.instHeader.innerHTML = `<div class="inst-head empty">Выберите инструмент в списке слева</div>`;
    return;
  }
  const changeCls = signClass(inst.change);
  refs.instHeader.innerHTML = `
    <div class="inst-left">
      <button type="button" class="inst-search icon-btn" data-action="focus-search" title="Поиск инструмента (/)" aria-label="Поиск инструмента">${icon('search', 18)}</button>
      <div class="inst-title">
        <div class="inst-name-row">
          <h1>${esc(inst.shortName || inst.name)}</h1>
          <span class="inst-code muted">${esc((inst.board || 'TQBR') + ':' + (inst.seccode || parseSymbol(inst.symbol).seccode))}</span>
        </div>
        <div class="inst-meta muted">
          <span class="board-pill">MOEX</span>
          <span>${esc(inst.board || 'TQBR')}</span>
          <span class="dot-sep">·</span>
          <span>${fmtHM(inst.updatedAt)}</span>
          <span class="dot-sep">·</span>
          <span class="src-pill">${store.get().mode === 'live' ? 'live' : 'снапшот'}</span>
        </div>
      </div>
    </div>
    <div class="inst-quote">
      <div class="inst-price" data-role="inst-price">${fmtRub(inst.last, inst.decimals)}</div>
      <div class="inst-change ${changeCls}" data-role="inst-change">
        ${fmtSignedRub(inst.change, inst.decimals)} <span class="chg-pct">(${fmtSignedPct(inst.changePct)})</span>
      </div>
    </div>
    <div class="inst-actions">
      <button type="button" class="icon-btn star-btn ${favorites.has(inst.symbol) ? 'active' : ''}" data-action="star" title="В избранное" aria-label="В избранное" aria-pressed="${favorites.has(inst.symbol)}">
        ${icon(favorites.has(inst.symbol) ? 'starFilled' : 'star', 18)}
      </button>
      <button type="button" class="btn btn-ai" data-action="open-ticket">${icon('plus', 15)} Заявка</button>
      <button type="button" class="btn btn-outline" disabled title="Опционы — во второй итерации">Опционы</button>
    </div>`;
}

function updateHeaderQuote(inst) {
  const priceEl = refs.instHeader.querySelector('[data-role="inst-price"]');
  const changeEl = refs.instHeader.querySelector('[data-role="inst-change"]');
  if (priceEl) priceEl.textContent = fmtRub(inst.last, inst.decimals);
  if (changeEl) {
    changeEl.className = `inst-change ${signClass(inst.change)}`;
    changeEl.innerHTML = `${fmtSignedRub(inst.change, inst.decimals)} <span class="chg-pct">(${fmtSignedPct(inst.changePct)})</span>`;
  }
}

/** Converts chart bar seconds into an RFC3339 UTC timestamp. */
function isoFromSeconds(seconds) {
  const value = Number(seconds);
  if (!Number.isFinite(value)) return '';
  return new Date(value * 1000).toISOString();
}

/** The visible screen sent with a chat request (plan Task 8, ScreenContext). */
function buildScreenContext() {
  const state = store.get();
  const tab = state.ui ? (state.ui.drawerOpen ? state.ui.drawerTab : state.ui.rightTab) : '';
  const context = {
    symbol: state.selected || '',
    periodSec: state.periodSec || 0,
    activeTab: tab || '',
  };
  const candles = state.candles || [];
  if (candles.length) {
    context.from = isoFromSeconds(candles[0].t);
    context.to = isoFromSeconds(candles[candles.length - 1].t);
  } else if (chart) {
    const visible = chart.getVisibleRange ? chart.getVisibleRange() : null;
    if (visible) {
      context.from = isoFromSeconds(visible.from);
      context.to = isoFromSeconds(visible.to);
    }
  }
  const selection = state.chartSelection;
  if (selection && Number.isFinite(Number(selection.from)) && Number.isFinite(Number(selection.to))) {
    context.selection = {
      from: isoFromSeconds(selection.from),
      to: isoFromSeconds(selection.to),
    };
  }
  return context;
}

function renderToolbar() {
  const state = store.get();
  const overlay = state.overlay;
  refs.chartToolbar.innerHTML = `
    <div class="periods" role="group" aria-label="Период графика">
      ${PERIODS.map((period) => `<button type="button" class="period-btn ${state.periodSec === period.sec ? 'active' : ''}"
        data-period="${period.sec}" ${state.periodSec === period.sec ? 'aria-pressed="true"' : ''}>${period.label}</button>`).join('')}
    </div>
    <span class="toolbar-sep"></span>
    <div class="chart-types" role="group" aria-label="Тип графика">
      <button type="button" class="tool-btn ${chartType === 'candles' ? 'active' : ''}" data-chart-type="candles" title="Свечи">${icon('candles', 17)}</button>
      <button type="button" class="tool-btn ${chartType === 'line' ? 'active' : ''}" data-chart-type="line" title="Линия">${icon('lineChart', 17)}</button>
    </div>
    <span class="toolbar-sep"></span>
    <div class="chart-tools" role="group" aria-label="Индикаторы">
      <button type="button" class="tool-btn" data-action="soon" data-soon="Индикаторы" title="Индикаторы — скоро">${icon('sliders', 17)}</button>
      <button type="button" class="tool-btn" data-action="soon" data-soon="Сравнение" title="Сравнение — скоро">${icon('grid', 17)}</button>
      <button type="button" class="tool-btn" data-action="soon" data-soon="Сигналы" title="Сигналы — скоро">${icon('bolt', 17)}</button>
    </div>
    <div class="toolbar-right">
      ${state.chartSelection ? `<button type="button" class="btn btn-ai btn-sm" data-ask-selection title="Спросить AI о выделенном диапазоне">
        ${icon('chat', 13)} Спросить AI о выделенном</button>
        <button type="button" class="tool-btn" data-clear-selection title="Снять выделение" aria-label="Снять выделение">${icon('close', 12)}</button>` : ''}
      ${overlay ? `<span class="overlay-chip">${icon('lineChart', 13)} ${esc(strategyTitle(overlay.strategy))} · ${fmtSignedPct(overlay.metrics.totalReturnPct)}
        <button type="button" data-action="clear-overlay" title="Снять с графика" aria-label="Снять бэктест с графика">${icon('close', 12)}</button></span>` : ''}
      <button type="button" class="tool-btn" data-action="soon" data-soon="Развернуть график" title="Развернуть — скоро">${icon('expand', 16)}</button>
    </div>`;
  if (overlay) {
    refs.chartOverlayBadge.hidden = true;
  }
}

function renderOhlc() {
  const state = store.get();
  const candles = state.candles;
  if (!candles.length) {
    refs.chartOhlc.innerHTML = '';
    return;
  }
  const inst = currentInstrument();
  const decimals = inst ? inst.decimals : 2;
  const last = candles[candles.length - 1];
  const prev = candles.length > 1 ? candles[candles.length - 2] : last;
  const chg = last.c - prev.c;
  const chgPct = prev.c ? (chg / prev.c) * 100 : 0;
  const cls = last.c >= last.o ? 'up' : 'down';
  refs.chartOhlc.innerHTML = `
    <div class="ohlc-values ${cls}">
      <span class="ohlc"><i>O</i><b>${fmtPrice(last.o, decimals)}</b></span>
      <span class="ohlc"><i>H</i><b>${fmtPrice(last.h, decimals)}</b></span>
      <span class="ohlc"><i>L</i><b>${fmtPrice(last.l, decimals)}</b></span>
      <span class="ohlc"><i>C</i><b>${fmtPrice(last.c, decimals)}</b></span>
      <span class="ohlc-chg ${signClass(chg)}">${arrow(chg)} ${fmtNum(Math.abs(chg), decimals)} (${fmtPct(Math.abs(chgPct))})</span>
      <span class="ohlc"><i>V</i><b>${fmtVolume(last.v)}</b></span>
    </div>
    <div class="ohlc-legend muted">
      <span class="legend-name">${esc(inst ? (inst.shortName || inst.name) : '')}</span>
      <span class="dot-sep">·</span>
      <span>${esc(inst ? inst.symbol : '')}</span>
      <span class="dot-sep">·</span>
      <span>${periodLabel(state.periodSec)}</span>
      ${state.historyMeta && state.historyMeta.note ? `<span class="dot-sep">·</span><span>${esc(state.historyMeta.note)}</span>` : ''}
    </div>`;
}

function renderRightTabs() {
  const state = store.get();
  const tabs = [
    { id: 'orderbook', title: 'Стакан' },
    { id: 'ticket', title: 'Торговля' },
    { id: 'trades', title: 'Торги' },
    { id: 'info', title: 'Инфо' },
    { id: 'dividends', title: 'Дивиденды' },
  ];
  refs.rightTabs.innerHTML = `
    <div class="right-tab-strip" role="tablist" aria-label="Данные по инструменту">
      ${tabs.map((tab) => `<button type="button" class="right-tab ${state.ui.rightTab === tab.id ? 'active' : ''}"
        data-right-tab="${tab.id}" role="tab" aria-selected="${state.ui.rightTab === tab.id}">${tab.title}</button>`).join('')}
    </div>
    <div class="right-tab-body" id="rightTabBody"></div>`;
  renderRightTabBody();
}

function renderRightTabBody() {
  const body = document.getElementById('rightTabBody');
  if (!body) return;
  const state = store.get();
  switch (state.ui.rightTab) {
    case 'ticket':
      renderTicket(body);
      break;
    case 'trades':
      renderTradesTab(body);
      break;
    case 'info':
      renderInfoTab(body);
      break;
    case 'dividends':
      renderDividendsTab(body);
      break;
    case 'orderbook':
    default:
      renderOrderbookTab(body);
      break;
  }
}

function renderOrderbookTab(body) {
  const state = store.get();
  const inst = currentInstrument();
  if (!state.orderbook) {
    body.innerHTML = `<div class="tab-loading"><span class="spinner"></span> Загрузка стакана…</div>`;
    return;
  }
  const orderbook = state.orderbook;
  const bids = orderbook.bids || [];
  const asks = orderbook.asks || [];
  const maxQty = Math.max(1, ...bids.map((row) => row.qty), ...asks.map((row) => row.qty));
  const spread = asks.length && bids.length ? asks[0].price - bids[0].price : 0;
  const spreadPct = spreadsPct(spread, asks.length ? asks[0].price : 0);
  const rows = Math.max(bids.length, asks.length);
  const bodyRows = [];
  for (let i = 0; i < rows; i += 1) {
    const bid = bids[i];
    const ask = asks[i];
    bodyRows.push(`
      <tr class="ob-row" data-ob-bid="${bid ? bid.price : ''}" data-ob-ask="${ask ? ask.price : ''}" tabindex="0">
        <td class="ob-bid">${bid ? fmtPrice(bid.price, inst ? inst.decimals : 2) : ''}</td>
        <td class="ob-vol">
          <span class="ob-bar bid" style="width:${bid ? (bid.qty / maxQty) * 50 : 0}%"></span>
          <span class="ob-bar ask" style="width:${ask ? (ask.qty / maxQty) * 50 : 0}%"></span>
          <span class="ob-vol-text">${bid ? fmtQty(bid.qty) : ''}${ask ? ` · ${fmtQty(ask.qty)}` : ''}</span>
        </td>
        <td class="ob-ask">${ask ? fmtPrice(ask.price, inst ? inst.decimals : 2) : ''}</td>
      </tr>`);
  }
  body.innerHTML = `
    <div class="ob-title">
      <span>Стакан · ${esc(inst ? inst.seccode : '')}</span>
      <span class="muted">${fmtHM(orderbook.time)}</span>
    </div>
    <table class="ob-table">
      <thead><tr><th>Bid, ₽</th><th class="ob-vol-head">Vol</th><th class="right">Ask, ₽</th></tr></thead>
      <tbody>${bodyRows.join('')}</tbody>
    </table>
    <div class="ob-foot">
      <span>Спред <b>${fmtPrice(spread, inst ? inst.decimals : 2)} ₽</b> (${fmtPct(spreadPct)})</span>
      <span class="muted">клик по цене → в тикет</span>
    </div>`;
}

function spreadsPct(spread, price) {
  return price ? (spread / price) * 100 : 0;
}

function inputPrice(value, decimals) {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(decimals) : '';
}

function resetTicket() {
  const inst = currentInstrument();
  ticket.side = 'buy';
  ticket.type = 'market';
  ticket.qty = inst ? Math.max(1, inst.lotSize || 1) : 1;
  ticket.price = inst ? String(inst.last) : '';
  ticket.touched = false;
}

function renderTicket(body) {
  const inst = currentInstrument();
  if (!inst) {
    body.innerHTML = `<div class="empty-state small">Выберите инструмент</div>`;
    return;
  }
  const decimals = inst.decimals;
  const price = ticket.type === 'market' ? inst.last : (Number(ticket.price) || inst.last);
  const cost = ticket.qty * price;
  const fee = cost * 0.0005;
  const isBuy = ticket.side === 'buy';
  body.innerHTML = `
    <div class="ticket">
      <div class="ticket-symbol">
        <span class="wl-badge" style="--badge:${badgeColor(inst.symbol)}">${esc(badgeLetters(inst))}</span>
        <div><b>${esc(inst.shortName || inst.name)}</b><span class="muted">${esc(inst.symbol)}</span></div>
        <span class="paper-badge" title="Бумажный счёт: реальные ордера не отправляются">Бумажный счёт</span>
      </div>
      <div class="seg seg-side" role="group" aria-label="Направление заявки">
        <button type="button" class="seg-btn buy ${isBuy ? 'active' : ''}" data-ticket-side="buy">Купить</button>
        <button type="button" class="seg-btn sell ${!isBuy ? 'active' : ''}" data-ticket-side="sell">Продать</button>
      </div>
      <div class="ticket-row">
        <label class="field"><span>Количество, шт</span>
          <div class="stepper">
            <button type="button" data-ticket-step="-1" aria-label="Уменьшить количество">−</button>
            <input id="ticketQty" type="number" min="1" step="${inst.lotSize || 1}" value="${ticket.qty}" inputmode="numeric">
            <button type="button" data-ticket-step="1" aria-label="Увеличить количество">+</button>
          </div>
        </label>
        <label class="field"><span>Тип заявки</span>
          <div class="seg seg-type">
            <button type="button" class="seg-btn ${ticket.type === 'market' ? 'active' : ''}" data-ticket-type="market">Рынок</button>
            <button type="button" class="seg-btn ${ticket.type === 'limit' ? 'active' : ''}" data-ticket-type="limit">Лимит</button>
          </div>
        </label>
      </div>
      <label class="field"><span>Цена, ₽</span>
        <input id="ticketPrice" type="number" step="${inst.minStep || 0.01}" min="0"
          value="${ticket.type === 'limit' ? inputPrice(price, decimals) : inputPrice(inst.last, decimals)}"
          ${ticket.type === 'market' ? 'disabled' : ''} aria-label="Цена заявки">
      </label>
      <div class="ticket-calc">
        <div><span>Стоимость</span><b>${fmtRub(cost)}</b></div>
        <div><span>Комиссия ~0,05%</span><b>${fmtRub(fee)}</b></div>
        <div class="total"><span>Итого</span><b>${fmtRub(cost + (isBuy ? fee : -fee))}</b></div>
      </div>
      <button type="button" class="btn ticket-submit ${isBuy ? 'btn-buy' : 'btn-sell'}" data-ticket-submit>
        ${isBuy ? 'Купить' : 'Продать'} ${esc(inst.seccode)} · ${fmtQty(ticket.qty)} шт
      </button>
      <div class="ticket-hint muted">
        ${icon('info', 12)} Исполнение по рынку, комиссия 0,05%. Лимитные заявки исполняются при касании цены.
      </div>
    </div>`;
}

function renderTradesTab(body) {
  const state = store.get();
  const inst = currentInstrument();
  const decimals = inst ? inst.decimals : 2;
  const trades = state.trades || [];
  body.innerHTML = `
    <div class="ob-title"><span>Лента сделок · ${esc(inst ? inst.seccode : '')}</span><span class="muted">${trades.length ? 'обновляется' : ''}</span></div>
    ${trades.length ? `
      <table class="flow-table">
        <thead><tr><th>Время</th><th class="right">Цена, ₽</th><th class="right">Объём</th></tr></thead>
        <tbody>
          ${trades.slice(0, 40).map((trade) => {
            const side = trade.side === 'buy' ? 'up' : trade.side === 'sell' ? 'down' : 'flat';
            return `<tr><td class="muted">${fmtHM(trade.time)}</td>
              <td class="right ${side}">${fmtPrice(trade.price, decimals)}</td>
              <td class="right">${fmtQty(trade.qty)}</td></tr>`;
          }).join('')}
        </tbody>
      </table>` : `<div class="tab-loading"><span class="spinner"></span> Загрузка ленты…</div>`}`;
}

function renderInfoTab(body) {
  const inst = currentInstrument();
  const state = store.get();
  if (!inst) {
    body.innerHTML = `<div class="empty-state small">Выберите инструмент</div>`;
    return;
  }
  const hints = state.hints;
  const rows = [
    ['Тикер', `${inst.seccode} · ${inst.board}`],
    ['Биржа', 'MOEX'],
    ['Валюта', inst.currency || 'RUB'],
    ['Лот', `${fmtQty(inst.lotSize)} шт`],
    ['Шаг цены', fmtPrice(inst.minStep, inst.decimals) + ' ₽'],
    ['Дневной диапазон', `${fmtPrice(inst.low, inst.decimals)} — ${fmtPrice(inst.high, inst.decimals)}`],
    ['Пред. закрытие', fmtPrice(inst.prevClose, inst.decimals) + ' ₽'],
    ['Открытие', fmtPrice(inst.open, inst.decimals) + ' ₽'],
    ['Объём за день', fmtVolume(inst.volume)],
  ];
  body.innerHTML = `
    <div class="info-block">
      <table class="info-table">
        ${rows.map(([label, value]) => `<tr><td>${label}</td><td class="right">${value}</td></tr>`).join('')}
      </table>
    </div>
    <div class="info-hints">
      <div class="info-hints-head">${icon('bulb', 14)} AI-подсказки <span class="muted">${periodLabel(state.periodSec)}</span></div>
      ${hints ? `
        <div class="hints-grid">
          <div><span>SMA20</span><b>${fmtPrice(hints.sma20, inst.decimals)}</b></div>
          <div><span>SMA50</span><b>${fmtPrice(hints.sma50, inst.decimals)}</b></div>
          <div><span>RSI14</span><b>${fmtNum(hints.rsi14, 1)}</b></div>
          <div><span>ATR14</span><b>${fmtPrice(hints.atr14, inst.decimals)}</b></div>
          <div><span>Поддержка</span><b>${fmtPrice(hints.support, inst.decimals)}</b></div>
          <div><span>Сопротивление</span><b>${fmtPrice(hints.resistance, inst.decimals)}</b></div>
        </div>
        <div class="hints-trend">
          <span class="badge-trend ${hints.trend === 'up' ? 'up' : hints.trend === 'down' ? 'down' : 'flat'}">
            ${hints.trend === 'up' ? 'Восходящий тренд' : hints.trend === 'down' ? 'Нисходящий тренд' : 'Боковой тренд'}
          </span>
          <span class="muted">ширина рынка: ${hints.breadth ? `${hints.breadth.advancers} ↑ / ${hints.breadth.decliners} ↓` : '—'}</span>
        </div>
        <div class="muted small">${esc(hints.note || '')}</div>` : `<div class="tab-loading"><span class="spinner"></span> Расчёт подсказок…</div>`}
    </div>`;
}

function renderDividendsTab(body) {
  const inst = currentInstrument();
  body.innerHTML = `
    <div class="empty-state tall">
      ${icon('calendar', 26)}
      <h4>Дивиденды</h4>
      <p>Нет данных о дивидендах${inst ? ` по ${esc(inst.seccode)}` : ''}.</p>
      <p class="muted small">Календарь дивидендов появится во второй итерации.</p>
    </div>`;
}

// ---------------------------------------------------------------------------
// bottom panel
// ---------------------------------------------------------------------------

function renderBottomSummary() {
  const state = store.get();
  const portfolio = state.portfolio;
  const collapsed = state.ui.bottomCollapsed;
  const dayCls = portfolio ? signClass(portfolio.dayChange) : 'flat';
  refs.bottomPanel.classList.toggle('collapsed', collapsed);
  const cashEl = document.getElementById('wlAccountCash');
  if (cashEl) cashEl.textContent = `свободно ${portfolio ? fmtRub(portfolio.cash) : '—'}`;
  refs.bottomSummary.innerHTML = `
    <div class="bs-account">
      <span class="account-chip">
        <span class="account-dot"></span>
        <b>ДЕМО</b> · ИИС3 2024
      </span>
      <span class="muted small bs-account-sub">бумажный счёт</span>
    </div>
    <div class="bs-block">
      <span class="bs-label">Стоимость портфеля</span>
      <b class="bs-value big">${portfolio ? fmtRub(portfolio.totalValue) : '—'}</b>
    </div>
    <div class="bs-block">
      <span class="bs-label">За день</span>
      <b class="bs-value ${dayCls}">${portfolio ? fmtSignedRub(portfolio.dayChange) : '—'}</b>
      <span class="bs-sub ${dayCls}">${portfolio ? fmtSignedPct(portfolio.dayChangePct) : ''}</span>
    </div>
    <div class="bs-block">
      <span class="bs-label">Свободные деньги</span>
      <b class="bs-value">${portfolio ? fmtRub(portfolio.cash) : '—'}</b>
    </div>
    <div class="bs-margin">
      <div class="gauge ${marginClass(portfolio ? portfolio.marginPct : 100)}" style="--pct:${clamp((portfolio ? portfolio.marginPct : 100) / 2, 0, 100)}">
        <span>${portfolio ? fmtNum(portfolio.marginPct, 0) + '%' : '—'}</span>
      </div>
      <span class="bs-label">Маржа</span>
    </div>
    <button type="button" class="icon-btn small bs-collapse" data-action="toggle-bottom" title="${collapsed ? 'Развернуть панель' : 'Свернуть панель'}" aria-label="${collapsed ? 'Развернуть панель' : 'Свернуть панель'}">
      ${icon(collapsed ? 'chevronDown' : 'chevronRight', 15)}
    </button>`;
  renderBottomTabs();
}

function marginClass(marginPct) {
  if (!Number.isFinite(marginPct)) return 'gauge-ok';
  if (marginPct < 50) return 'gauge-danger';
  if (marginPct < 100) return 'gauge-warn';
  return 'gauge-ok';
}

function renderBottomTabs() {
  const state = store.get();
  const tabs = [
    { id: 'portfolio', title: 'Портфель' },
    { id: 'orders', title: 'Заявки', badge: (state.orders || []).filter((order) => order.status === 'new').length },
    { id: 'history', title: 'История' },
    { id: 'analytics', title: 'Аналитика' },
  ];
  refs.bottomTabs.innerHTML = `
    <div class="bt-tabs" role="tablist" aria-label="Нижняя панель">
      ${tabs.map((tab) => `<button type="button" class="bt-tab ${state.ui.bottomTab === tab.id ? 'active' : ''}"
        data-bottom-tab="${tab.id}" role="tab" aria-selected="${state.ui.bottomTab === tab.id}">${tab.title}${tab.badge ? ` <span class="tab-badge">${tab.badge}</span>` : ''}</button>`).join('')}
    </div>
    <div class="bt-right muted small">
      ${state.portfolio ? `Обновлено ${fmtHM(state.portfolio.updatedAt)}` : ''}
    </div>`;
}

function switchBottomTab(tab) {
  store.patchGroup('ui', { bottomTab: tab, bottomCollapsed: false });
  refs.bottomPanel.classList.remove('collapsed');
  renderBottomSummary();
  renderBottomBody();
}

function renderBottomBody() {
  const state = store.get();
  switch (state.ui.bottomTab) {
    case 'orders':
      renderOrdersTable();
      break;
    case 'history':
      renderHistoryTable();
      break;
    case 'analytics':
      renderAnalytics();
      break;
    case 'portfolio':
    default:
      renderPortfolioTable();
      break;
  }
}

function renderPortfolioTable() {
  const state = store.get();
  const portfolio = state.portfolio;
  if (!portfolio) {
    refs.bottomBody.innerHTML = `<div class="tab-loading"><span class="spinner"></span> Загрузка портфеля…</div>`;
    return;
  }
  const positions = portfolio.positions || [];
  if (!positions.length) {
    refs.bottomBody.innerHTML = `<div class="empty-state small">Портфель пуст — купите первый инструмент на вкладке «Торговля».</div>`;
    return;
  }
  const instMap = new Map(state.instruments.map((inst) => [inst.symbol, inst]));
  refs.bottomBody.innerHTML = `
    <table class="p-table">
      <thead><tr>
        <th class="col-instrument">Инструмент</th><th>Биржа</th><th class="right">Кол-во</th>
        <th class="right">Цена</th><th class="right">Изм.</th><th class="right">Оценка</th>
        <th class="right">Обеспеч.</th><th class="right">Средняя ц.</th>
        <th class="right">Прибыль за день</th><th class="right">Прибыль</th><th class="right">Прибыль %</th><th></th>
      </tr></thead>
      <tbody>
        ${positions.map((position) => {
          const inst = instMap.get(position.symbol);
          const prevClose = inst ? inst.prevClose : position.price;
          const dayPnl = (position.price - prevClose) * position.qty;
          const decimals = inst ? inst.decimals : 2;
          const name = position.name || (inst ? inst.shortName || inst.name : position.symbol);
          const seccode = parseSymbol(position.symbol).seccode;
          return `<tr class="clickable" data-symbol="${esc(position.symbol)}">
            <td class="col-instrument"><span class="wl-badge sm" style="--badge:${badgeColor(position.symbol)}">${esc(seccode.slice(0, 2))}</span>
              <span class="p-name"><b>${esc(name)}</b><span class="muted">${esc(seccode)}</span></span></td>
            <td class="muted">MOEX</td>
            <td class="right">${fmtQty(position.qty)}</td>
            <td class="right">${fmtPrice(position.price, decimals)}</td>
            <td class="right ${signClass(position.dayChangePct)}">${fmtSignedPct(position.dayChangePct)}</td>
            <td class="right">${fmtRub(position.value, decimals)}</td>
            <td class="right muted">${fmtRub(position.value, decimals)}</td>
            <td class="right">${fmtPrice(position.avgPrice, decimals)}</td>
            <td class="right ${signClass(dayPnl)}">${fmtSignedRub(dayPnl, decimals)}</td>
            <td class="right ${signClass(position.profit)}">${fmtSignedRub(position.profit, decimals)}</td>
            <td class="right ${signClass(position.profitPct)}">${fmtSignedPct(position.profitPct)}</td>
            <td class="right"><button type="button" class="icon-btn tiny" data-action="soon" data-soon="Меню позиции" title="Действия — скоро">${icon('dots', 15)}</button></td>
          </tr>`;
        }).join('')}
      </tbody>
      <tfoot>
        <tr class="p-footer">
          <td colspan="5"><b>Свободные деньги</b></td>
          <td class="right">${fmtRub(portfolio.cash)}</td>
          <td colspan="2" class="muted">Реализовано: <b>${fmtSignedRub(portfolio.realizedPnl)}</b></td>
          <td class="right muted">Комиссии: ${fmtRub(portfolio.fees)}</td>
          <td colspan="2" class="right"><b>Итого: ${fmtRub(portfolio.totalValue)}</b></td>
          <td></td>
        </tr>
      </tfoot>
    </table>`;
}

function renderOrdersTable() {
  const state = store.get();
  const orders = state.orders || [];
  if (!orders.length) {
    refs.bottomBody.innerHTML = `<div class="empty-state small">
      ${icon('candles', 24)}<p>Заявок нет. Создайте первую на вкладке «Торговля» справа от графика.</p></div>`;
    return;
  }
  refs.bottomBody.innerHTML = `
    <table class="p-table orders-table">
      <thead><tr>
        <th>Время</th><th class="col-instrument">Инструмент</th><th>Направление</th><th>Тип</th>
        <th class="right">Кол-во</th><th class="right">Цена</th><th class="right">Исполнено</th>
        <th>Статус</th><th>Источник</th><th></th>
      </tr></thead>
      <tbody>
        ${orders.map((order) => {
          const seccode = parseSymbol(order.symbol).seccode;
          const sideCls = order.side === 'buy' ? 'up' : 'down';
          const cancelable = order.status === 'new';
          return `<tr>
            <td class="muted">${fmtHM(order.createdAt)}</td>
            <td class="col-instrument"><span class="wl-badge sm" style="--badge:${badgeColor(order.symbol)}">${esc(seccode.slice(0, 2))}</span>${esc(seccode)}</td>
            <td class="${sideCls}">${SIDE_TITLES[order.side] || order.side}</td>
            <td class="muted">${order.type === 'limit' ? 'Лимит' : 'Рынок'}</td>
            <td class="right">${fmtQty(order.qty)}</td>
            <td class="right">${order.type === 'market' ? 'по рынку' : fmtRub(order.price)}</td>
            <td class="right">${fmtQty(order.filled)}</td>
            <td><span class="status-pill status-${esc(order.status)}">${esc(STATUS_TITLES[order.status] || order.status)}</span></td>
            <td class="muted">${esc(SOURCE_TITLES[order.source] || order.source || '—')}</td>
            <td class="right">${cancelable ? `<button type="button" class="btn btn-ghost btn-xs" data-order-cancel="${esc(order.id)}">Отменить</button>` : ''}</td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>`;
}

function renderHistoryTable() {
  const state = store.get();
  const rows = state.tradeHistory || [];
  if (!rows.length) {
    refs.bottomBody.innerHTML = `<div class="empty-state small">${icon('clock', 24)}<p>Сделок пока нет.</p></div>`;
    return;
  }
  refs.bottomBody.innerHTML = `
    <table class="p-table">
      <thead><tr>
        <th>Время</th><th class="col-instrument">Инструмент</th><th>Направление</th>
        <th class="right">Кол-во</th><th class="right">Цена</th><th class="right">Комиссия</th>
        <th class="right">Сумма</th><th>Источник</th>
      </tr></thead>
      <tbody>
        ${rows.map((trade) => {
          const seccode = parseSymbol(trade.symbol).seccode;
          const sideCls = trade.side === 'buy' ? 'up' : 'down';
          const inst = state.instruments.find((row) => row.symbol === trade.symbol);
          const decimals = inst ? inst.decimals : 2;
          return `<tr>
            <td class="muted">${fmtHM(trade.time)}</td>
            <td class="col-instrument"><span class="wl-badge sm" style="--badge:${badgeColor(trade.symbol)}">${esc(seccode.slice(0, 2))}</span>${esc(seccode)}</td>
            <td class="${sideCls}">${SIDE_TITLES[trade.side] || trade.side}</td>
            <td class="right">${fmtQty(trade.qty)}</td>
            <td class="right">${fmtPrice(trade.price, decimals)}</td>
            <td class="right muted">${fmtRub(trade.fee)}</td>
            <td class="right">${fmtRub(trade.qty * trade.price)}</td>
            <td class="muted">${esc(SOURCE_TITLES[trade.source] || trade.source || '—')}</td>
          </tr>`;
        }).join('')}
      </tbody>
    </table>`;
}

function renderAnalytics() {
  const state = store.get();
  analyticsCharts = [];
  const seenResults = new Set();
  const results = [];
  for (const result of state.backtests || []) {
    const key = `${result.symbol}|${result.strategy}|${result.periodSec}`;
    if (seenResults.has(key)) continue;
    seenResults.add(key);
    results.push(result);
    if (results.length >= 4) break;
  }
  if (!results.length) {
    refs.bottomBody.innerHTML = `<div class="empty-state small">
      ${icon('lineChart', 24)}<p>Бэктестов пока нет. Попросите AI-агента: «протестируй лучшую стратегию на Сбербанке».</p></div>`;
    return;
  }
  refs.bottomBody.innerHTML = `<div class="analytics-grid">
    ${results.slice(0, 4).map((result, index) => {
      const metrics = result.metrics || {};
      const inst = state.instruments.find((row) => row.symbol === result.symbol);
      const name = inst ? (inst.shortName || inst.name) : result.symbol;
      const retCls = metrics.totalReturnPct >= 0 ? 'up' : 'down';
      return `<div class="analytics-card">
        <div class="analytics-head">
          <div><b>${esc(strategyTitle(result.strategy))}</b> <span class="muted">· ${esc(name)}</span>
            <div class="muted small">${fmtDayMonth(metrics.from)} — ${fmtDayMonth(metrics.to)} · ${periodLabel(result.periodSec)} · ${fmtQty(metrics.bars)} бар.</div>
          </div>
          <button type="button" class="btn btn-ghost btn-xs" data-backtest-chart="${index}">${icon('eye', 13)} На график</button>
        </div>
        <div class="analytics-metrics">
          <span>Доходность <b class="${retCls}">${fmtSignedPct(metrics.totalReturnPct)}</b></span>
          <span>Просадка <b class="down">${fmtPct(metrics.maxDrawdownPct)}</b></span>
          <span>Sharpe <b>${fmtNum(metrics.sharpe, 2)}</b></span>
          <span>Сделки <b>${fmtQty(metrics.trades)}</b></span>
          <span>Винрейт <b>${fmtPct(metrics.winRatePct, 1)}</b></span>
        </div>
        <div class="analytics-equity"><canvas data-analytics-canvas="${index}"></canvas></div>
      </div>`;
    }).join('')}
  </div>`;
  for (const result of results.slice(0, 4)) {
    const index = results.indexOf(result);
    const canvas = refs.bottomBody.querySelector(`canvas[data-analytics-canvas="${index}"]`);
    if (!canvas) continue;
    const equityChart = new EquityChart(canvas);
    equityChart.setData(result);
    analyticsCharts.push(equityChart);
  }
}

// ---------------------------------------------------------------------------
// data loading
// ---------------------------------------------------------------------------

async function loadSymbol(symbol, periodSec) {
  if (!symbol) return;
  const token = ++loadToken;
  const inst = store.get().instruments.find((row) => row.symbol === symbol);
  selectedSymbol = symbol;
  if (!ticket.touched) resetTicket();
  store.set({
    selected: symbol,
    periodSec,
    candlesLoading: true,
    orderbook: null,
    trades: [],
    hints: null,
  });
  renderWatchlist();
  renderInstrumentHeader();
  renderToolbar();
  renderOhlc();
  renderRightTabs();
  refs.chartLoading.hidden = false;
  refs.chartEmpty.hidden = true;
  try {
    const history = await api.candles(symbol, periodSec, 260);
    if (token !== loadToken) return;
    store.set({ candles: history.candles, historyMeta: history, candlesLoading: false });
    chart.setData({ candles: history.candles, instrument: inst, periodSec });
    if (store.get().overlay) chart.setOverlay(store.get().overlay);
    renderOhlc();
    renderToolbar();
    renderOverlayBadge();
    refs.chartLoading.hidden = true;
    if (!history.candles.length) {
      refs.chartEmpty.hidden = false;
      refs.chartEmpty.innerHTML = `<div class="empty-state small">${icon('candles', 22)}<p>Нет истории за выбранный период</p></div>`;
    }
  } catch (error) {
    if (token !== loadToken) return;
    store.set({ candlesLoading: false });
    refs.chartLoading.hidden = true;
    refs.chartEmpty.hidden = false;
    refs.chartEmpty.innerHTML = `<div class="empty-state small">${icon('warning', 22)}<p>Не удалось загрузить историю</p><span class="muted small">${esc(error.message || '')}</span></div>`;
    toast('error', `История: ${error.message || 'ошибка загрузки'}`);
  }
  if (token !== loadToken) return;
  loadOrderbook(symbol);
  if (store.get().ui.rightTab === 'trades') loadTrades();
  if (store.get().ui.rightTab === 'info') loadHints();
  refreshBottomRelated();
}

async function loadOrderbook(symbol) {
  try {
    const orderbook = await api.orderbook(symbol);
    if (store.get().selected !== symbol) return;
    store.set({ orderbook });
    if (store.get().ui.rightTab === 'orderbook') renderRightTabBody();
  } catch { /* orderbook is non-critical */ }
}

async function loadTrades() {
  const state = store.get();
  if (!state.selected) return;
  try {
    const trades = await api.trades(state.selected, 60);
    if (store.get().selected !== state.selected) return;
    store.set({ trades });
    if (store.get().ui.rightTab === 'trades') renderRightTabBody();
  } catch { /* non-critical */ }
}

async function loadHints() {
  const state = store.get();
  if (!state.selected) return;
  try {
    const hints = await api.hints(state.selected, state.periodSec);
    if (store.get().selected !== state.selected) return;
    store.set({ hints });
    if (store.get().ui.rightTab === 'info') renderRightTabBody();
  } catch { /* non-critical */ }
}

async function refreshBottomRelated() {
  try {
    const [portfolio, orders, tradeHistory] = await Promise.all([
      api.portfolio(), api.orders(), api.tradeHistory(),
    ]);
    store.set({ portfolio, orders, tradeHistory });
    renderBottomSummary();
    renderBottomBody();
  } catch { /* non-critical */ }
}

function refreshAllData() {
  startPollingOnce();
  refreshBottomRelated();
  const state = store.get();
  if (state.selected) loadSymbol(state.selected, state.periodSec);
}

async function startPollingOnce() {
  try {
    const [instrumentsPayload, portfolio, orders] = await Promise.all([
      api.instruments(), api.portfolio(), api.orders(),
    ]);
    store.set({ instruments: instrumentsPayload.instruments || store.get().instruments, portfolio, orders });
    renderWatchlist();
    renderBottomSummary();
    renderBottomBody();
  } catch (error) {
    toast('error', `Не удалось обновить данные: ${error.message || error}`);
  }
}

// ---------------------------------------------------------------------------
// server events (SSE)
// ---------------------------------------------------------------------------

function handleServerEvent(message) {
  const type = message.type;
  const data = message.data;
  switch (type) {
    case '_sse': {
      sseState = data.state;
      updateConnection();
      break;
    }
    case 'quote': {
      applyQuote(data);
      break;
    }
    case 'portfolio': {
      if (!data) break;
      store.set({ portfolio: data });
      renderBottomSummary();
      if (store.get().ui.bottomTab === 'portfolio') renderBottomBody();
      break;
    }
    case 'orders': {
      if (!data) break;
      store.set({ orders: data.orders || [] });
      renderBottomTabs();
      if (store.get().ui.bottomTab === 'orders') renderBottomBody();
      break;
    }
    case 'status': {
      if (!data) break;
      applyHealth(Object.assign({}, store.get().health || {}, {
        ok: data.ok, mode: data.mode || store.get().mode,
        connected: data.connected, message: data.message,
        serverTime: data.serverTime,
      }));
      break;
    }
    case 'agent': {
      ai?.handleAgentEvent(data);
      break;
    }
    case 'proposal': {
      ai?.handleProposalEvent(data);
      break;
    }
    default:
      break;
  }
}

function applyQuote(data) {
  if (!data || !data.symbol) return;
  const state = store.get();
  const inst = state.instruments.find((row) => row.symbol === data.symbol);
  if (!inst) return;
  inst.last = Number(data.last) || inst.last;
  inst.change = Number(data.change) || 0;
  inst.changePct = Number(data.changePct) || 0;
  if (data.bid) inst.bid = Number(data.bid);
  if (data.ask) inst.ask = Number(data.ask);
  if (data.volume) inst.volume = Number(data.volume);
  inst.updatedAt = data.time || new Date().toISOString();
  if (Array.isArray(inst.spark)) {
    inst.spark.push(inst.last);
    if (inst.spark.length > 60) inst.spark.shift();
  }
  updateWatchRow(inst);
  if (state.selected === data.symbol) {
    updateHeaderQuote(inst);
    updateChartLast(inst.last);
    renderOhlc();
    if (state.ui.rightTab === 'ticket' && !ticket.touched) {
      const priceInput = document.getElementById('ticketPrice');
      if (priceInput && ticket.type === 'market') priceInput.value = inputPrice(inst.last, inst.decimals);
    }
  }
}

function updateChartLast(price) {
  if (!chart || !chart.candles.length) return;
  if (chartType === 'line') {
    chart.candles[chart.candles.length - 1].c = price;
  } else {
    const last = chart.candles[chart.candles.length - 1];
    last.c = price;
    last.h = Math.max(last.h, price);
    last.l = Math.min(last.l, price);
  }
  chart.draw();
}

function refreshBottomTabsBadge() {
  const state = store.get();
  const badge = refs.bottomTabs.querySelector('.tab-badge');
  const active = (state.orders || []).filter((order) => order.status === 'new').length;
  if (active && badge) badge.textContent = String(active);
  else if (active) renderBottomTabs();
}

// ---------------------------------------------------------------------------
// interactions
// ---------------------------------------------------------------------------

function bindEvents() {
  document.addEventListener('click', (event) => {
    if (event.target.closest('#aiDrawer')) return;
    handleClick(event);
  });
  document.addEventListener('keydown', (event) => {
    const target = event.target;
    const typing = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT');
    if (event.key === 'Escape') {
      if (ai && store.get().ui.drawerOpen) {
        ai.close();
      } else if (typing) {
        target.blur();
      }
      return;
    }
    if (!typing && (event.key === '/' || ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k'))) {
      event.preventDefault();
      focusSearch();
    }
  });
  document.addEventListener('input', (event) => {
    const target = event.target;
    if (target.id === 'watchFilter' || target.id === 'globalSearch') {
      const value = target.value;
      store.patchGroup('ui', { watchFilter: value });
      syncFilterInputs(value);
      clearTimeout(watchFilterTimer);
      watchFilterTimer = setTimeout(() => {
        if (refs.watchlistList.querySelector('.wl-row') || value.trim()) {
          renderWatchlistListOnly();
        }
      }, 120);
    }
    if (target.id === 'ticketQty') {
      ticket.qty = Math.max(1, Math.round(Number(target.value) || 1));
      ticket.touched = true;
      updateTicketCalc();
    }
    if (target.id === 'ticketPrice') {
      ticket.price = target.value;
      ticket.touched = true;
      updateTicketCalc();
    }
  });
  document.addEventListener('change', (event) => {
    const target = event.target;
    if (target.id === 'ticketQty' || target.id === 'ticketPrice') {
      updateTicketCalc();
    }
  });
  document.getElementById('aiBtn').addEventListener('click', () => ai?.toggle());
  document.getElementById('notifBtn').addEventListener('click', () => toast('info', 'Уведомления — скоро'));
  document.getElementById('helpBtn').addEventListener('click', () => {
    toast('info', 'AI Terminal: «/» — поиск, Esc — закрыть панель. Все операции — на бумажном счёте.');
  });
  store.on((type, data) => {
    const patch = (data && data.patch) || {};
    if (patch.ui && ('drawerOpen' in patch.ui || 'page' in patch.ui)) renderHeader();
    if ('overlay' in patch) {
      renderToolbar();
      renderOverlayBadge();
    }
  });
}

function syncFilterInputs(value) {
  const watch = document.getElementById('watchFilter');
  const global = document.getElementById('globalSearch');
  if (watch && watch.value !== value) watch.value = value;
  if (global && global.value !== value && document.activeElement !== global) global.value = value;
}

function renderWatchlistListOnly() {
  const state = store.get();
  const scrollTop = refs.watchlistList.scrollTop;
  renderWatchlist();
  refs.watchlistList.scrollTop = scrollTop;
  void state;
}

function focusSearch() {
  const input = document.getElementById('globalSearch');
  if (input) {
    input.focus();
    input.select();
  }
}

function handleClick(event) {
  const target = event.target;

  const rail = target.closest('[data-rail]');
  if (rail) {
    handleRail(rail.dataset.rail);
    return;
  }

  const watchRow = target.closest('.wl-row[data-symbol]');
  if (watchRow) {
    selectSymbol(watchRow.dataset.symbol);
    return;
  }
  const positionRow = target.closest('tr.clickable[data-symbol]');
  if (positionRow && !target.closest('button')) {
    selectSymbol(positionRow.dataset.symbol);
    return;
  }

  const period = target.closest('[data-period]');
  if (period) {
    const periodSec = Number(period.dataset.period);
    if (periodSec && periodSec !== store.get().periodSec) loadSymbol(store.get().selected, periodSec);
    return;
  }

  const chartTypeBtn = target.closest('[data-chart-type]');
  if (chartTypeBtn) {
    chartType = chartTypeBtn.dataset.chartType === 'line' ? 'line' : 'candles';
    chart.type = chartType;
    renderToolbar();
    chart.draw();
    return;
  }

  const askSelection = target.closest('[data-ask-selection]');
  if (askSelection) {
    if (store.get().chartSelection && ai) {
      ai.open('chat');
      ai.send('Объясни выделенный диапазон на графике: что здесь произошло и на что обратить внимание?');
    }
    return;
  }

  const clearSelection = target.closest('[data-clear-selection]');
  if (clearSelection) {
    chart?.clearSelection();
    return;
  }

  const rightTab = target.closest('[data-right-tab]');
  if (rightTab) {
    store.patchGroup('ui', { rightTab: rightTab.dataset.rightTab });
    renderRightTabs();
    if (rightTab.dataset.rightTab === 'trades') loadTrades();
    if (rightTab.dataset.rightTab === 'info') loadHints();
    if (rightTab.dataset.rightTab === 'orderbook') loadOrderbook(store.get().selected);
    return;
  }

  const bottomTab = target.closest('[data-bottom-tab]');
  if (bottomTab) {
    switchBottomTab(bottomTab.dataset.bottomTab);
    return;
  }

  const obRow = target.closest('.ob-row');
  if (obRow) {
    const bid = obRow.dataset.obBid;
    const ask = obRow.dataset.obAsk;
    if (ask) {
      ticket.side = 'buy';
      ticket.type = 'limit';
      ticket.price = ask;
    } else if (bid) {
      ticket.side = 'sell';
      ticket.type = 'limit';
      ticket.price = bid;
    }
    ticket.touched = true;
    store.patchGroup('ui', { rightTab: 'ticket' });
    renderRightTabs();
    toast('info', `Цена ${ticket.price} ₽ подставлена в заявку`);
    return;
  }

  const ticketSide = target.closest('[data-ticket-side]');
  if (ticketSide) {
    ticket.side = ticketSide.dataset.ticketSide;
    renderTicket(document.getElementById('rightTabBody'));
    return;
  }
  const ticketType = target.closest('[data-ticket-type]');
  if (ticketType) {
    ticket.type = ticketType.dataset.ticketType;
    const inst = currentInstrument();
    if (ticket.type === 'limit' && inst) ticket.price = String(inst.last);
    renderTicket(document.getElementById('rightTabBody'));
    return;
  }
  const ticketStep = target.closest('[data-ticket-step]');
  if (ticketStep) {
    const inst = currentInstrument();
    const step = inst ? Math.max(1, inst.lotSize || 1) : 1;
    ticket.qty = Math.max(1, ticket.qty + Number(ticketStep.dataset.ticketStep) * step);
    ticket.touched = true;
    const qtyInput = document.getElementById('ticketQty');
    if (qtyInput) qtyInput.value = String(ticket.qty);
    updateTicketCalc();
    return;
  }
  const ticketSubmit = target.closest('[data-ticket-submit]');
  if (ticketSubmit) {
    submitOrder(ticketSubmit);
    return;
  }

  const cancelBtn = target.closest('[data-order-cancel]');
  if (cancelBtn) {
    cancelOrder(cancelBtn.dataset.orderCancel, cancelBtn);
    return;
  }

  const backtestChart = target.closest('[data-backtest-chart]');
  if (backtestChart) {
    const results = store.get().backtests || [];
    const result = results[Number(backtestChart.dataset.backtestChart)];
    if (result && ai) ai.showBacktestOnChart(result);
    return;
  }

  const actionEl = target.closest('[data-action]');
  if (!actionEl) return;
  const action = actionEl.dataset.action;
  switch (action) {
    case 'soon':
      toast('info', `${actionEl.dataset.soon || 'Раздел'} — скоро`);
      break;
    case 'focus-search':
      focusSearch();
      break;
    case 'refresh-data':
      refreshAllData();
      toast('info', 'Данные обновляются…');
      break;
    case 'account-menu':
      toast('info', 'Счета и портфели — во второй итерации');
      break;
    case 'star': {
      const inst = currentInstrument();
      if (!inst) break;
      if (favorites.has(inst.symbol)) favorites.delete(inst.symbol);
      else favorites.add(inst.symbol);
      renderInstrumentHeader();
      renderWatchlist();
      toast('success', favorites.has(inst.symbol) ? `${inst.seccode} добавлен в избранное` : `${inst.seccode} убран из избранного`);
      break;
    }
    case 'open-ticket':
      store.patchGroup('ui', { rightTab: 'ticket' });
      resetTicket();
      renderRightTabs();
      break;
    case 'toggle-bottom':
      store.patchGroup('ui', { bottomCollapsed: !store.get().ui.bottomCollapsed });
      renderBottomSummary();
      break;
    case 'clear-overlay':
      chart.clearOverlay();
      store.set({ overlay: null });
      renderToolbar();
      renderOverlayBadge();
      break;
    case 'retry':
      window.location.reload();
      break;
    default:
      break;
  }
}

function handleRail(sectionId) {
  const section = RAIL_SECTIONS.find((row) => row.id === sectionId);
  if (!section) return;
  if (section.soon) {
    toast('info', `Раздел «${section.title}» — скоро`);
    return;
  }
  store.patchGroup('ui', { page: sectionId });
  renderRail();
  if (sectionId === 'chat') ai?.open('chat');
  if (sectionId === 'portfolio') switchBottomTab('portfolio');
  if (sectionId === 'favorites') focusSearch();
}

function updateTicketCalc() {
  const body = document.getElementById('rightTabBody');
  if (!body || store.get().ui.rightTab !== 'ticket') return;
  const inst = currentInstrument();
  if (!inst) return;
  const price = ticket.type === 'market' ? inst.last : (Number(ticket.price) || inst.last);
  const cost = ticket.qty * price;
  const fee = cost * 0.0005;
  const calc = body.querySelector('.ticket-calc');
  const submit = body.querySelector('[data-ticket-submit]');
  if (calc) {
    calc.innerHTML = `
      <div><span>Стоимость</span><b>${fmtRub(cost)}</b></div>
      <div><span>Комиссия ~0,05%</span><b>${fmtRub(fee)}</b></div>
      <div class="total"><span>Итого</span><b>${fmtRub(cost + (ticket.side === 'buy' ? fee : -fee))}</b></div>`;
  }
  if (submit) {
    submit.innerHTML = `${ticket.side === 'buy' ? 'Купить' : 'Продать'} ${esc(inst.seccode)} · ${fmtQty(ticket.qty)} шт`;
  }
}

async function submitOrder(button) {
  const inst = currentInstrument();
  if (!inst) return;
  const payload = {
    symbol: inst.symbol,
    side: ticket.side,
    qty: ticket.qty,
    type: ticket.type,
  };
  if (ticket.type === 'limit') payload.price = Number(ticket.price) || inst.last;
  button.disabled = true;
  const original = button.innerHTML;
  button.innerHTML = `<span class="spinner small"></span> Отправка…`;
  try {
    const order = await api.placeOrder(payload);
    const status = (order && order.status) || 'new';
    if (status === 'filled') {
      toast('success', `${SIDE_TITLES[ticket.side]} ${fmtQty(ticket.qty)} ${inst.seccode} исполнена по ${fmtRub(order.avgFillPrice || order.price || inst.last, inst.decimals)}`);
    } else if (status === 'new') {
      toast('success', `Заявка выставлена: ${SIDE_TITLES[ticket.side].toLowerCase()} ${fmtQty(ticket.qty)} ${inst.seccode} по ${fmtRub(payload.price, inst.decimals)}`);
    } else {
      toast('info', `Заявка: ${STATUS_TITLES[status] || status}`);
    }
    await refreshBottomRelated();
    if (store.get().ui.rightTab === 'ticket') renderRightTabBody();
    if (store.get().ui.rightTab === 'trades') loadTrades();
    if (store.get().ui.rightTab === 'orderbook') loadOrderbook(inst.symbol);
  } catch (error) {
    toast('error', `Заявка отклонена: ${error.message || error}`);
    button.disabled = false;
    button.innerHTML = original;
  }
}

async function cancelOrder(id, button) {
  button.disabled = true;
  button.textContent = '…';
  try {
    await api.cancelOrder(id);
    toast('success', 'Заявка отменена');
    await refreshBottomRelated();
  } catch (error) {
    toast('error', `Не удалось отменить заявку: ${error.message || error}`);
    button.disabled = false;
    button.textContent = 'Отменить';
  }
}

function selectSymbol(symbol) {
  if (!symbol || symbol === store.get().selected) return;
  const periodSec = store.get().periodSec;
  store.set({ overlay: null });
  chart.clearOverlay();
  loadSymbol(symbol, periodSec);
}

function renderOverlayBadge() {
  const state = store.get();
  refs.chartOverlayBadge.hidden = true;
  void state;
}

// ---------------------------------------------------------------------------
// misc rendering
// ---------------------------------------------------------------------------

/**
 * Deterministic demo deep links (also used by acceptance screenshots):
 *   ?view=ai|autonomy  — open the AI drawer / its autonomy tab
 *   ?ask=<text>        — send a chat message through the real API after boot
 *   ?tab=analytics     — open the bottom panel tab (portfolio|orders|history|analytics)
 *   ?symbol=TQBR:SBER  — preselect an instrument
 *   ?panel=ai          — legacy alias of ?view=ai
 */
function applyDeepLinks() {
  const params = new URLSearchParams(window.location.search);
  const view = (params.get('view') || params.get('panel') || '').toLowerCase();
  const ask = (params.get('ask') || '').trim();
  const tab = (params.get('tab') || '').toLowerCase();
  const symbol = params.get('symbol');
  if (symbol && store.get().instruments.some((inst) => inst.symbol === symbol)) {
    selectSymbol(symbol);
  }
  if (tab === 'portfolio' || tab === 'orders' || tab === 'history' || tab === 'analytics') {
    switchBottomTab(tab);
  }
  const drawerTab = view === 'autonomy' ? 'autonomy' : 'chat';
  if (view === 'ai' || view === 'drawer' || view === 'chat' || view === 'autonomy' || ask) {
    setTimeout(() => ai?.open(drawerTab), 300);
  }
  if (ask) {
    setTimeout(() => ai?.send(ask), 900);
  }
}

function renderAll() {
  renderHeader();
  renderRail();
  renderWatchlistHead();
  renderWatchlist();
  renderInstrumentHeader();
  renderToolbar();
  renderOhlc();
  renderRightTabs();
  renderBottomSummary();
  renderBottomBody();
  renderOverlayBadge();
  setStatusClock();
  updateConnection();
}

function toast(type, message) {
  if (!refs.toasts) return;
  const icons = { success: 'check', error: 'warning', info: 'info' };
  const element = document.createElement('div');
  element.className = `toast toast-${type}`;
  element.innerHTML = `<span class="toast-icon">${icon(icons[type] || 'info', 16)}</span><span class="toast-text">${esc(message)}</span>
    <button type="button" class="toast-close" aria-label="Закрыть уведомление">${icon('close', 13)}</button>`;
  refs.toasts.appendChild(element);
  const remove = () => {
    element.classList.add('out');
    setTimeout(() => element.remove(), 220);
  };
  element.querySelector('.toast-close').addEventListener('click', remove);
  setTimeout(remove, type === 'error' ? 6500 : 4200);
}

function cssEscape(value) {
  if (window.CSS && typeof window.CSS.escape === 'function') return window.CSS.escape(value);
  return String(value).replace(/[^a-zA-Z0-9_-]/g, (ch) => `\\${ch}`);
}

// keep imports referenced by render helpers (tree-shake friendly for reviewers)
void LOG_KIND_TITLES;
void fmtDate;
void fmtDateTime;
void pluralRu;
void on;
void debounce;

boot();
