// ai.js — the right-hand AI drawer: chat with the agent, tool-call cards,
// backtest artifacts (metrics + equity + "показать на графике"), and the
// autonomy panel (paper replay) with live status/log via SSE.

import { EquityChart } from './chart.js';
import { icon, sparkSvg } from './icons.js';
import {
  LOG_KIND_TITLES, SOURCE_TITLES, STRATEGY_TITLES, PERIODS, clamp, esc, isValidTime,
  fmtDate, fmtDateTime, fmtHM, fmtNum, fmtPct, fmtPrice, fmtRub, fmtSignedPct,
  periodLabel, pluralRu, strategyTitle, timeAgo,
} from './utils.js';

function formatAnswer(text) {
  const escaped = esc(String(text || '')).replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  return escaped
    .split(/\n{2,}/)
    .map((part) => `<p>${part.replace(/\n/g, '<br>')}</p>`)
    .join('');
}

function pretty(value, max = 1200) {
  try {
    const text = JSON.stringify(value, null, 1);
    if (!text) return '';
    return text.length > max ? `${text.slice(0, max)}\n…` : text;
  } catch {
    return String(value);
  }
}

function toolSummary(call) {
  const args = call.args || {};
  const parts = [];
  if (args.symbol) parts.push(args.symbol.split(':').pop());
  if (args.periodSec) parts.push(periodLabel(args.periodSec));
  if (args.strategy) parts.push(strategyTitle(args.strategy));
  if (args.count) parts.push(`${args.count} бар.`);
  if (args.side) parts.push(args.side === 'buy' ? 'покупка' : 'продажа');
  return parts.join(' · ');
}

const TOOL_TITLES = {
  list_instruments: 'Список инструментов',
  get_market_hints: 'Рыночные подсказки',
  get_history: 'История баров',
  run_backtest: 'Бэктест стратегии',
  compare_strategies: 'Сравнение стратегий',
  get_quote: 'Котировка и стакан',
  get_portfolio: 'Портфель',
  get_strategies: 'Список стратегий',
  place_order: 'Бумажная заявка',
  cancel_order: 'Отмена заявки',
  start_autonomy: 'Запуск автономии',
  stop_autonomy: 'Остановка автономии',
  get_agent_status: 'Статус агента',
  get_backtest_history: 'История бэктестов',
};

const PROPOSAL_STATUS_TITLES = {
  pending: 'ждёт подтверждения',
  approved: 'подтверждено',
  executed: 'выполнено',
  rejected: 'отклонено',
  expired: 'истекло',
};

const QUICK_PROMPTS = [
  'Протестируй лучшую стратегию на Сбербанке за месяц',
  'Запусти автоторговлю на SBER',
  'Что с моим портфелем?',
  'Покажи стакан и уровень поддержки',
];

export class AIDrawer {
  constructor({ root, backdrop, store, api, chart, onToast = () => {}, onOpenBacktests = () => {} }) {
    this.root = root;
    this.backdrop = backdrop;
    this.store = store;
    this.api = api;
    this.chart = chart;
    this.onToast = onToast;
    this.onOpenBacktests = onOpenBacktests;
    this.equityCharts = [];
    // Dialog id of the bounded conversation history kept by the backend. The
    // first request sends none and stores the id the response returns.
    this.conversationId = '';
    this.formRendered = false;
    this.mount();
    this.unsubscribe = this.store.on((type, data) => this.onStore(type, data));
  }

  mount() {
    this.root.innerHTML = `
      <div class="drawer-head">
        <div class="drawer-title">
          <span class="ai-avatar big">${sparkSvg(18)}</span>
          <div class="drawer-title-text">
            <h2>AI-агент</h2>
            <div class="drawer-sub"><span class="llm-chip" id="llmChip">локальный аналитик</span></div>
          </div>
        </div>
        <button type="button" class="icon-btn" data-drawer-close aria-label="Закрыть AI-панель" title="Закрыть (Esc)">${icon('close', 18)}</button>
      </div>
      <div class="drawer-tabs" role="tablist">
        <button type="button" class="drawer-tab active" data-drawer-tab="chat" role="tab">Чат</button>
        <button type="button" class="drawer-tab" data-drawer-tab="autonomy" role="tab">
          Автономия <span class="autonomy-dot" id="autonomyDot" hidden></span>
        </button>
      </div>
      <div class="drawer-pane" data-pane="chat">
        <div class="quick-prompts" id="quickPrompts"></div>
        <div class="chat-log" id="chatLog"></div>
        <div class="agent-activity" id="agentActivity" hidden></div>
        <form class="chat-form" id="chatForm">
          <textarea id="chatInput" rows="1" placeholder="Спросите AI-агента: бэктест, автоторговля, портфель…" aria-label="Сообщение AI-агенту"></textarea>
          <button type="submit" class="btn btn-ai btn-send" id="chatSend" aria-label="Отправить сообщение">${icon('send', 17)}</button>
        </form>
        <div class="drawer-foot muted">Локальный аналитик работает офлайн на реальных данных терминала. Не является инвестиционной рекомендацией.</div>
      </div>
      <div class="drawer-pane" data-pane="autonomy" hidden>
        <div class="auto-intro">
          <div class="auto-intro-title">${icon('bolt', 16)} Автоторговля на бумажном счёте</div>
          <div class="muted small">Replay исторических баров с риск-лимитами. Реальные ордера не отправляются.</div>
        </div>
        <div id="autoForm"></div>
        <div id="autoState"></div>
      </div>`;
    this.root.setAttribute('aria-hidden', 'true');
    this.bind();
  }

  bind() {
    this.root.addEventListener('click', (event) => {
      const target = event.target;
      if (target.closest('[data-drawer-close]')) {
        this.close();
        return;
      }
      const tab = target.closest('[data-drawer-tab]');
      if (tab) {
        this.setTab(tab.dataset.drawerTab);
        return;
      }
      const prompt = target.closest('[data-quick-prompt]');
      if (prompt) {
        const text = prompt.dataset.quickPrompt;
        const input = this.root.querySelector('#chatInput');
        if (input) input.value = text;
        this.send(text);
        return;
      }
      const backtestBtn = target.closest('[data-show-backtest]');
      if (backtestBtn) {
        const msgIndex = Number(backtestBtn.dataset.msg);
        const artIndex = Number(backtestBtn.dataset.art);
        const artifact = this.store.get().chat[msgIndex]?.artifacts?.[artIndex];
        if (artifact && artifact.kind === 'backtest') this.showBacktestOnChart(artifact.payload);
        return;
      }
      const autoTab = target.closest('[data-open-autonomy]');
      if (autoTab) {
        this.setTab('autonomy');
        return;
      }
      const stopAutonomy = target.closest('[data-autonomy-stop]');
      if (stopAutonomy) this.stopAutonomy();
      const approveProposal = target.closest('[data-proposal-approve]');
      if (approveProposal) {
        this.resolveProposal(approveProposal.dataset.proposalApprove, true);
        return;
      }
      const rejectProposal = target.closest('[data-proposal-reject]');
      if (rejectProposal) {
        this.resolveProposal(rejectProposal.dataset.proposalReject, false);
        return;
      }
    });

    this.root.addEventListener('submit', (event) => {
      if (event.target.id !== 'chatForm') {
        if (event.target.id === 'autonomyForm') {
          event.preventDefault();
          this.startAutonomy();
        }
        return;
      }
      event.preventDefault();
      const input = this.root.querySelector('#chatInput');
      const text = (input?.value || '').trim();
      if (!text) return;
      input.value = '';
      this.send(text);
    });

    this.root.querySelector('#chatInput').addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        this.root.querySelector('#chatForm').requestSubmit();
      }
    });

    this.backdrop?.addEventListener('click', () => this.close());
  }

  // ---- open / close / tabs --------------------------------------------

  open(tab = 'chat') {
    this.root.classList.add('open');
    this.root.setAttribute('aria-hidden', 'false');
    document.body.classList.add('drawer-open');
    if (this.backdrop) this.backdrop.hidden = false;
    this.store.patchGroup('ui', { drawerOpen: true });
    if (tab) this.setTab(tab);
    this.renderQuickPrompts();
    this.renderChat();
    this.refreshProposals();
    this.renderForm();
    this.renderAutonomyState();
    setTimeout(() => this.root.querySelector('#chatInput')?.focus(), 120);
  }

  close() {
    this.root.classList.remove('open');
    this.root.setAttribute('aria-hidden', 'true');
    document.body.classList.remove('drawer-open');
    if (this.backdrop) this.backdrop.hidden = true;
    this.store.patchGroup('ui', { drawerOpen: false });
  }

  toggle() {
    if (this.root.classList.contains('open')) this.close();
    else this.open();
  }

  setTab(tab) {
    const value = tab === 'autonomy' ? 'autonomy' : 'chat';
    this.root.querySelectorAll('.drawer-tab').forEach((button) => {
      button.classList.toggle('active', button.dataset.drawerTab === value);
    });
    this.root.querySelectorAll('.drawer-pane').forEach((pane) => {
      pane.hidden = pane.dataset.pane !== value;
    });
    this.store.patchGroup('ui', { drawerTab: value });
    if (value === 'autonomy') {
      this.renderForm();
      this.renderAutonomyState();
    }
    if (value === 'chat') this.scrollChat();
  }

  // ---- store reactions -------------------------------------------------

  onStore(type, data) {
    const { patch } = data || {};
    if (!patch) return;
    if ('autonomy' in patch || 'autonomyLog' in patch) this.scheduleAutonomyRender();
    if ('agentStatus' in patch) this.renderLlmChip();
    if ('chat' in patch || 'chatBusy' in patch || 'agentActivity' in patch) this.renderChat();
    if ('proposals' in patch) this.renderChat();
    if ('strategies' in patch || 'instruments' in patch) this.renderForm();
    if ('backtests' in patch) this.renderAutonomyState();
    if (patch.ui && patch.ui.drawerOpen) {
      if (this.isOpen()) this.renderQuickPrompts();
      if (this.isOpen()) this.renderChat();
      if (this.isOpen()) this.renderForm();
      if (this.isOpen()) this.renderAutonomyState();
    }
  }

  isOpen() {
    return this.root.classList.contains('open');
  }

  renderLlmChip() {
    const chip = this.root.querySelector('#llmChip');
    if (!chip) return;
    const status = this.store.get().agentStatus;
    const llm = (status && status.llm) || this.store.get().llm;
    if (llm && llm.enabled) {
      chip.textContent = llm.model ? `LLM: ${llm.model}` : 'LLM';
      chip.classList.add('on');
    } else {
      chip.textContent = (llm && llm.note) || 'локальный аналитик';
      chip.classList.remove('on');
    }
  }

  renderQuickPrompts() {
    const container = this.root.querySelector('#quickPrompts');
    if (!container) return;
    const selected = this.store.get().instruments.find((inst) => inst.symbol === this.store.get().selected);
    const name = selected ? selected.seccode : '';
    container.innerHTML = QUICK_PROMPTS.map((text) => {
      const filled = text.replace('Сбербанке', name || 'Сбербанке');
      return `<button type="button" class="quick-prompt" data-quick-prompt="${esc(filled)}">${esc(filled)}</button>`;
    }).join('');
    this.renderLlmChip();
  }

  // ---- chat ------------------------------------------------------------

  renderChat() {
    const log = this.root.querySelector('#chatLog');
    if (!log) return;
    const state = this.store.get();
    const messages = state.chat || [];
    if (!messages.length) {
      log.innerHTML = `
        <div class="chat-empty">
          <div class="chat-empty-icon">${sparkSvg(26)}</div>
          <h3>Чем помочь?</h3>
          <p>Я вижу котировки, стакан, историю и портфель. Могу прогнать бэктест стратегий,
          подсказать уровни или запустить автоторговлю на бумажном счёте.</p>
        </div>`;
    } else {
      log.innerHTML = messages.map((message, index) => this.renderMessage(message, index)).join('');
    }
    if (state.chatBusy) {
      log.insertAdjacentHTML('beforeend', `
        <div class="chat-msg ai typing">
          <span class="ai-avatar">${sparkSvg(14)}</span>
          <div class="ai-body"><div class="typing-dots"><i></i><i></i><i></i></div></div>
        </div>`);
    }
    const proposals = state.proposals || [];
    if (proposals.length) log.insertAdjacentHTML('beforeend', this.renderProposals(proposals));
    const activity = this.root.querySelector('#agentActivity');
    if (activity) {
      if (state.agentActivity) {
        activity.hidden = false;
        activity.innerHTML = `${icon('bolt', 13)} ${esc(state.agentActivity)}`;
      } else {
        activity.hidden = true;
      }
    }
    this.mountEquityCharts();
    this.scrollChat();
  }

  renderMessage(message, index) {
    if (message.role === 'user') {
      return `<div class="chat-msg user"><div class="bubble">${esc(message.text).replace(/\n/g, '<br>')}</div></div>`;
    }
    if (message.role === 'system') {
      return `<div class="chat-msg system">${icon('warning', 14)} <span>${esc(message.text)}</span></div>`;
    }
    const toolCards = (message.toolCalls || []).map((call) => this.renderToolCard(call)).join('');
    const toolTotal = (message.toolCalls || []).reduce((sum, call) => sum + (call.durationMs || 0), 0);
    const tools = toolCards
      ? `<details class="tools-summary">
          <summary><span class="tool-icon">${icon('bolt', 13)}</span>
            <span class="tool-name">Инструменты агента · ${(message.toolCalls || []).length}</span>
            <span class="tool-status ok">${fmtNum(toolTotal, 0)} мс</span>
            ${icon('chevronDown', 13, 'tool-chev')}
          </summary>
          <div class="tools-body">${toolCards}</div>
        </details>`
      : '';
    // The backend may return several backtest artifacts per answer (compare + best run);
    // show one inline card and let the rest live in the bottom "Аналитика" tab.
    const shown = [];
    let skippedBacktests = 0;
    (message.artifacts || []).forEach((artifact, artIndex) => {
      if (artifact.kind === 'backtest') {
        if (shown.some((row) => row.artifact.kind === 'backtest')) {
          skippedBacktests += 1;
          return;
        }
      }
      shown.push({ artifact, artIndex });
    });
    const artifacts = shown.map(({ artifact, artIndex }) => {
      if (artifact.kind === 'backtest') return this.renderBacktestCard(artifact.payload, index, artIndex);
      if (artifact.kind === 'hints') return this.renderHintsCard(artifact.payload);
      if (artifact.kind === 'autonomy') return this.renderAutonomyCard(artifact.payload);
      return '';
    }).join('') + (skippedBacktests
      ? `<div class="artifacts-note muted small">${icon('info', 12)} Ещё ${skippedBacktests} бэктест(а) — во вкладке «Аналитика»</div>`
      : '');
    const meta = message.llm
      ? `LLM · ${fmtNum(message.elapsedMs || 0, 0)} мс`
      : `локальный аналитик${message.elapsedMs ? ` · ${fmtNum(message.elapsedMs, 0)} мс` : ''}`;
    return `
      <div class="chat-msg ai" data-msg-index="${index}">
        <span class="ai-avatar">${sparkSvg(14)}</span>
        <div class="ai-body">
          <div class="bubble ai-bubble">${formatAnswer(message.text)}</div>
          ${tools}
          ${artifacts}
          <div class="msg-meta">${esc(meta)}</div>
        </div>
      </div>`;
  }

  renderToolCard(call) {
    const title = TOOL_TITLES[call.name] || call.name;
    const summary = toolSummary(call);
    const error = call.error ? `<span class="tool-status error">ошибка</span>` : `<span class="tool-status ok">${icon('check', 11)} ${fmtNum(call.durationMs || 0, 0)} мс</span>`;
    return `
      <details class="tool-card">
        <summary>
          <span class="tool-icon">${icon('bolt', 13)}</span>
          <span class="tool-name">${esc(title)}</span>
          ${summary ? `<span class="tool-args">${esc(summary)}</span>` : ''}
          ${error}
          ${icon('chevronDown', 13, 'tool-chev')}
        </summary>
        <div class="tool-body">
          <div class="tool-label">Аргументы</div>
          <pre>${esc(pretty(call.args || {}))}</pre>
          <div class="tool-label">Результат${call.error ? `: ${esc(call.error)}` : ''}</div>
          <pre>${esc(pretty(call.result ?? (call.error ? { error: call.error } : {})))}</pre>
        </div>
      </details>`;
  }

  renderBacktestCard(result, msgIndex, artIndex) {
    const metrics = result.metrics || {};
    const name = this.instrumentName(result.symbol);
    const title = `${strategyTitle(result.strategy)} · ${name}`;
    const retCls = metrics.totalReturnPct >= 0 ? 'up' : 'down';
    const cells = [
      ['Доходность', fmtSignedPct(metrics.totalReturnPct), retCls],
      ['Бенчмарк', fmtSignedPct(metrics.benchmarkReturnPct), metrics.benchmarkReturnPct >= 0 ? 'up' : 'down'],
      ['Макс. просадка', fmtPct(metrics.maxDrawdownPct), 'down'],
      ['Sharpe', fmtNum(metrics.sharpe, 2), ''],
      ['Сделки', fmtNum(metrics.trades, 0), ''],
      ['Винрейт', fmtPct(metrics.winRatePct, 1), ''],
      ['Profit factor', fmtNum(metrics.profitFactor, 2), ''],
      ['Бары', `${periodLabel(result.periodSec)} · ${fmtNum(metrics.bars, 0)}`, ''],
    ];
    return `
      <div class="bt-card" data-bt="${artIndex}">
        <div class="bt-head">
          <div>
            <div class="bt-title">${icon('lineChart', 14)} Бэктест · ${esc(title)}</div>
            <div class="bt-sub muted">${fmtDate(metrics.from)} — ${fmtDate(metrics.to)} · комиссия 0,05% · проскальзывание 1 б.п.</div>
          </div>
          <button type="button" class="icon-btn small" data-show-backtest data-msg="${msgIndex}" data-art="${artIndex}" title="Показать equity и сделки на графике">${icon('eye', 15)}</button>
        </div>
        <div class="bt-metrics">
          ${cells.map(([label, value, cls]) => `<div class="bt-metric"><span>${esc(label)}</span><b class="${cls}">${esc(value)}</b></div>`).join('')}
        </div>
        <div class="bt-equity"><canvas data-bt-canvas="${msgIndex}:${artIndex}"></canvas></div>
        <div class="bt-actions">
          <button type="button" class="btn btn-ghost btn-sm" data-show-backtest data-msg="${msgIndex}" data-art="${artIndex}">${icon('eye', 14)} Показать на графике</button>
          <span class="muted small">${icon('info', 12)} прошлая доходность не гарантирует будущую</span>
        </div>
      </div>`;
  }

  mountEquityCharts() {
    this.equityCharts = [];
    const state = this.store.get();
    this.root.querySelectorAll('canvas[data-bt-canvas]').forEach((canvas) => {
      const [msgIndex, artIndex] = canvas.dataset.btCanvas.split(':').map(Number);
      const artifact = state.chat[msgIndex]?.artifacts?.[artIndex];
      if (!artifact || artifact.kind !== 'backtest') return;
      const chart = new EquityChart(canvas);
      chart.setData(artifact.payload);
      this.equityCharts.push(chart);
    });
  }

  renderHintsCard(hints) {
    const trend = hints.trend === 'up' ? 'восходящий' : hints.trend === 'down' ? 'нисходящий' : 'боковой';
    const trendCls = hints.trend === 'up' ? 'up' : hints.trend === 'down' ? 'down' : 'flat';
    return `
      <div class="hints-card compact">
        <div class="hints-head">${icon('bulb', 14)} AI-подсказки · ${esc((hints.symbol || '').split(':').pop())}
          <span class="badge-trend ${trendCls}">${trend}</span></div>
        <div class="hints-line">
          <span>SMA20 <b>${fmtPrice(hints.sma20, 2)}</b></span>
          <span>RSI14 <b>${fmtNum(hints.rsi14, 1)}</b></span>
          <span>Поддержка <b>${fmtPrice(hints.support, 2)}</b></span>
          <span>Сопротивление <b>${fmtPrice(hints.resistance, 2)}</b></span>
        </div>
      </div>`;
  }

  renderAutonomyCard(status) {
    const running = status && status.running;
    return `
      <div class="auto-artifact">
        <div class="auto-artifact-head">${icon('bolt', 14)} Автономия ${running ? '<span class="badge-live">идёт</span>' : '<span class="badge-muted">остановлена</span>'}</div>
        <div class="auto-artifact-row"><span>Инструмент</span><b>${esc((status?.config?.symbol || '').split(':').pop() || '—')}</b></div>
        <div class="auto-artifact-row"><span>Стратегия</span><b>${esc(strategyTitle(status?.config?.strategy))}</b></div>
        <div class="auto-artifact-row"><span>PnL</span><b class="${(status?.pnl || 0) >= 0 ? 'up' : 'down'}">${fmtRub(status?.pnl || 0)} (${fmtSignedPct(status?.pnlPct || 0)})</b></div>
        <button type="button" class="btn btn-ghost btn-sm" data-open-autonomy>Открыть автономию ${icon('chevronRight', 13)}</button>
      </div>`;
  }

  scrollChat() {
    const log = this.root.querySelector('#chatLog');
    if (log) log.scrollTop = log.scrollHeight;
  }

  /** Long answers: anchor the view on the start of the assistant message. */
  scrollChatToLastAnswer() {
    requestAnimationFrame(() => {
      const log = this.root.querySelector('#chatLog');
      if (!log) return;
      const last = log.querySelector('.chat-msg.ai[data-msg-index]');
      if (!last || log.scrollHeight <= log.clientHeight) return;
      const logRect = log.getBoundingClientRect();
      const lastRect = last.getBoundingClientRect();
      log.scrollTop += lastRect.top - logRect.top - 6;
    });
  }

  async send(text) {
    const state = this.store.get();
    if (state.chatBusy) return;
    const chat = [...state.chat, { role: 'user', text, at: new Date().toISOString() }];
    this.store.set({ chat, chatBusy: true, agentActivity: '' });
    this.renderChat();
    try {
      const payload = {
        message: text,
        symbol: state.selected,
        periodSec: state.periodSec,
      };
      if (this.conversationId) payload.conversationId = this.conversationId;
      const response = await this.api.agentChat(payload);
      if (response && response.conversationId) this.conversationId = response.conversationId;
      const assistant = {
        role: 'assistant',
        text: response.answer || 'Готово.',
        toolCalls: response.toolCalls || [],
        artifacts: response.artifacts || [],
        llm: !!response.llm,
        elapsedMs: response.elapsedMs || 0,
        at: new Date().toISOString(),
      };
      const nextChat = [...this.store.get().chat, assistant];
      this.store.set({ chat: nextChat, chatBusy: false, agentActivity: '' });
      this.afterArtifacts(assistant.artifacts || []);
      for (const call of assistant.toolCalls) {
        if (call && call.result && call.result.proposal) this.upsertProposal(call.result.proposal);
      }
      this.scrollChatToLastAnswer();
    } catch (error) {
      const message = error && error.message ? error.message : 'Не удалось получить ответ агента';
      this.store.set({
        chat: [...this.store.get().chat, { role: 'system', text: `Ошибка агента: ${message}` }],
        chatBusy: false,
        agentActivity: '',
      });
      this.onToast('error', `AI-агент: ${message}`);
    }
  }

  afterArtifacts(artifacts) {
    const backtests = artifacts.filter((artifact) => artifact.kind === 'backtest').map((artifact) => artifact.payload);
    if (backtests.length) {
      this.store.set({
        backtests: [...backtests, ...(this.store.get().backtests || [])],
      });
    }
    const autonomy = artifacts.find((artifact) => artifact.kind === 'autonomy');
    if (autonomy) {
      this.store.set({ autonomy: autonomy.payload, autonomyLog: autonomy.payload.log || [] });
      this.setTab('autonomy');
    }
  }

  showBacktestOnChart(result) {
    if (!result || !this.chart) return;
    this.chart.setOverlay(result);
    this.store.set({ overlay: result });
    this.onToast('info', `Стратегия «${strategyTitle(result.strategy)}» наложена на график`);
    document.querySelector('.chart-panel')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  // ---- agent SSE events (autonomy replay, live tool calls) -------------

  handleAgentEvent(data) {
    const inner = data || {};
    const kind = inner.type || inner.Type || '';
    const payload = inner.data !== undefined ? inner.data : (inner.Data !== undefined ? inner.Data : inner);
    if (kind === 'autonomy') {
      // Backend SSE sends a partial replay tick {running,symbol,periodSec,time,price,
      // position,trades,pnl,pnlPct}; GET /api/agent/status returns the full status.
      const tick = payload && typeof payload === 'object' ? payload : {};
      const current = this.store.get().autonomy || {};
      const next = Object.assign({}, current, {
        running: tick.running !== undefined ? !!tick.running : current.running,
        trades: tick.trades !== undefined ? tick.trades : current.trades,
        pnl: tick.pnl !== undefined ? tick.pnl : current.pnl,
        pnlPct: tick.pnlPct !== undefined ? tick.pnlPct : current.pnlPct,
      });
      if (tick.config) next.config = tick.config;
      if (!next.config && tick.symbol) {
        next.config = {
          symbol: tick.symbol, periodSec: tick.periodSec,
          strategy: (current.config && current.config.strategy) || 'sma_cross',
        };
      }
      if (Array.isArray(tick.log)) {
        next.log = tick.log;
        this.store.set({ autonomy: next, autonomyLog: tick.log, agentActivity: next.running ? 'автономия: replay исполняется…' : '' });
      } else {
        this.store.set({ autonomy: next, agentActivity: next.running ? 'автономия: replay исполняется…' : '' });
      }
      return;
    }
    if (kind === 'log') {
      const entry = payload && (payload.message || payload.kind) ? payload : { time: new Date().toISOString(), kind: 'info', message: String(payload) };
      const log = [...(this.store.get().autonomyLog || []), entry].slice(-120);
      const patch = { autonomyLog: log };
      if (entry.kind === 'stop') {
        const current = this.store.get().autonomy;
        const data = entry.data || {};
        if (current) {
          patch.autonomy = Object.assign({}, current, {
            running: false,
            trades: data.trades !== undefined ? data.trades : current.trades,
            pnl: data.pnl !== undefined ? data.pnl : current.pnl,
            pnlPct: data.pnlPct !== undefined ? data.pnlPct : current.pnlPct,
          });
        }
        patch.agentActivity = '';
      } else if (entry.kind === 'start') {
        patch.agentActivity = 'автономия: replay исполняется…';
      }
      this.store.set(patch);
      return;
    }
    if (kind === 'tool_call' || kind === 'tool_result') {
      const name = payload?.name || payload?.Name || '';
      const phase = kind === 'tool_call' ? 'вызывает' : 'получил';
      this.store.set({ agentActivity: `AI-агент ${phase} инструмент ${name}…` });
      return;
    }
    if (kind === 'backtest') {
      const result = payload?.result || payload;
      if (result && result.metrics) {
        this.store.set({ backtests: [result, ...(this.store.get().backtests || [])] });
      }
    }
  }

  // ---- human-in-the-loop proposals (R5) --------------------------------

  handleProposalEvent(proposal) {
    if (!proposal || !proposal.id) return;
    this.upsertProposal(proposal);
    if (proposal.status === 'pending') {
      this.onToast('info', `Нужно подтверждение: ${proposal.summary || proposal.tool}`);
    }
  }

  upsertProposal(proposal) {
    if (!proposal || !proposal.id) return;
    const current = this.store.get().proposals || [];
    const index = current.findIndex((row) => row.id === proposal.id);
    const next = index >= 0
      ? current.map((row, i) => (i === index ? Object.assign({}, row, proposal) : row))
      : [...current, proposal];
    this.store.set({ proposals: next.slice(-20) });
  }

  async refreshProposals() {
    try {
      const proposals = await this.api.agentProposals();
      this.store.set({ proposals: proposals.slice(-20) });
    } catch {
      // The SSE "proposal" stream keeps the list fresh on its own.
    }
  }

  async resolveProposal(id, approve) {
    try {
      const data = approve ? await this.api.approveProposal(id) : await this.api.rejectProposal(id);
      const proposal = data && data.proposal;
      if (proposal) this.upsertProposal(proposal);
      if (approve && data && data.result) {
        const result = data.result.result;
        if (result && result.config && result.config.symbol) {
          this.store.set({ autonomy: result, autonomyLog: result.log || [] });
        }
      }
      const title = proposal ? (proposal.summary || proposal.tool) : id;
      this.onToast(approve ? 'success' : 'info',
        approve ? `Подтверждено и выполнено: ${title}` : 'Предложение отклонено');
      this.renderChat();
    } catch (error) {
      this.onToast('error', `Не удалось обработать предложение: ${error.message || error}`);
      if (error && error.code === 'proposal_expired') this.refreshProposals();
    }
  }

  renderProposals(proposals) {
    const rows = proposals.slice(-4).map((proposal) => {
      const pending = proposal.status === 'pending';
      const actions = pending
        ? `<div class="auto-actions">
            <button type="button" class="btn btn-ai btn-sm" data-proposal-approve="${esc(proposal.id)}">${icon('check', 13)} Подтвердить</button>
            <button type="button" class="btn btn-danger-ghost btn-sm" data-proposal-reject="${esc(proposal.id)}">Отклонить</button>
          </div>`
        : `<span class="badge-${proposal.status === 'executed' ? 'live' : 'muted'}">${esc(PROPOSAL_STATUS_TITLES[proposal.status] || proposal.status)}</span>`;
      return `
        <div class="auto-artifact proposal-card" data-proposal-id="${esc(proposal.id)}">
          <div class="auto-artifact-head">${icon('shield', 14)} Предложение агента ${pending ? '<span class="badge-live">ждёт подтверждения</span>' : ''}</div>
          <div class="auto-artifact-row"><span>Действие</span><b>${esc(proposal.summary || proposal.tool || '')}</b></div>
          <div class="auto-artifact-row"><span>Действует до</span><b>${isValidTime(proposal.expiresAt) ? fmtHM(proposal.expiresAt) : '—'}</b></div>
          ${actions}
        </div>`;
    }).join('');
    return `<div class="chat-proposals">${rows}</div>`;
  }

  // ---- autonomy panel ---------------------------------------------------

  renderForm() {
    const container = this.root.querySelector('#autoForm');
    if (!container) return;
    const state = this.store.get();
    if (this.formRendered || !state.strategies.length || !state.instruments.length) return;
    this.formRendered = true;
    const selectedSymbol = state.selected || state.instruments[0].symbol;
    const symbolOptions = state.instruments.map((inst) => {
      const label = `${inst.seccode} · ${inst.shortName || inst.name}`;
      return `<option value="${esc(inst.symbol)}" ${inst.symbol === selectedSymbol ? 'selected' : ''}>${esc(label)}</option>`;
    }).join('');
    const strategyOptions = state.strategies.map((strategy) => (
      `<option value="${esc(strategy.id)}" ${strategy.id === 'sma_cross' ? 'selected' : ''}>${esc(strategy.name)}</option>`
    )).join('');
    const periodOptions = PERIODS.map((period) => (
      `<option value="${period.sec}" ${period.sec === state.periodSec ? 'selected' : ''}>${period.label}</option>`
    )).join('');
    const current = state.autonomy && state.autonomy.config ? state.autonomy.config : {};
    container.innerHTML = `
      <form class="auto-form" id="autonomyForm">
        <label class="field"><span>Инструмент</span><select name="symbol">${symbolOptions}</select></label>
        <div class="field-row">
          <label class="field"><span>Период</span><select name="periodSec">${periodOptions}</select></label>
          <label class="field"><span>Стратегия</span><select name="strategy">${strategyOptions}</select></label>
        </div>
        <div class="field-row">
          <label class="field"><span>Бюджет, ₽</span><input name="budget" type="number" min="10000" step="10000" value="${current.budget || 200000}"></label>
          <label class="field"><span>Скорость, бар/с</span><input name="speed" type="number" min="1" max="60" step="1" value="${current.speed || 5}"></label>
        </div>
        <div class="field-row three">
          <label class="field"><span>Макс. позиция, %</span><input name="maxPositionPct" type="number" min="1" max="100" value="${current.maxPositionPct || 25}"></label>
          <label class="field"><span>Стоп-лосс, %</span><input name="stopLossPct" type="number" min="0.1" max="50" step="0.1" value="${current.stopLossPct || 2}"></label>
          <label class="field"><span>Тейк-профит, %</span><input name="takeProfitPct" type="number" min="0.1" max="100" step="0.1" value="${current.takeProfitPct || 5}"></label>
        </div>
        <label class="field"><span>Дневной лимит убытка, %</span><input name="dailyLossPct" type="number" min="0.1" max="50" step="0.1" value="${current.dailyLossPct || 3}"></label>
        <div class="auto-actions">
          <button type="submit" class="btn btn-ai" data-autonomy-start>${icon('play', 15)} Старт автономии</button>
          <button type="button" class="btn btn-danger-ghost" data-autonomy-stop>${icon('stop', 13)} Стоп</button>
        </div>
        <div class="muted small auto-note">${icon('shield', 12)} Бумажный счёт · риск-лимиты проверяются перед каждой заявкой · kill-switch по стопу</div>
      </form>`;
  }

  scheduleAutonomyRender() {
    if (this.autonomyRenderTimer) return;
    this.autonomyRenderTimer = setTimeout(() => {
      this.autonomyRenderTimer = 0;
      this.renderAutonomyState();
    }, 120);
  }

  renderAutonomyState() {
    const container = this.root.querySelector('#autoState');
    if (!container) return;
    const status = this.store.get().autonomy;
    const log = this.store.get().autonomyLog || [];
    const running = !!(status && status.running);
    const dot = this.root.querySelector('#autonomyDot');
    if (dot) dot.hidden = !running;
    const pnl = (status && status.pnl) || 0;
    const pnlCls = pnl >= 0 ? 'up' : 'down';
    const startBtn = this.root.querySelector('[data-autonomy-start]');
    const stopBtn = this.root.querySelector('[data-autonomy-stop]');
    if (startBtn) startBtn.disabled = running;
    if (stopBtn) stopBtn.disabled = !running;
    const cfg = status && status.config ? status.config : {};
    const rows = log.slice(-10).reverse();
    container.innerHTML = `
      <div class="auto-status">
        <div class="auto-status-head">
          <span class="auto-state ${running ? 'running' : 'stopped'}">${running ? '● replay исполняется' : '○ остановлена'}</span>
          ${status && isValidTime(status.startedAt) ? `<span class="muted small">с ${fmtHM(status.startedAt)}</span>` : ''}
        </div>
        <div class="auto-stats">
          <div class="auto-stat"><span>PnL</span><b class="${pnlCls}">${fmtRub(pnl)}</b></div>
          <div class="auto-stat"><span>PnL %</span><b class="${pnlCls}">${fmtSignedPct((status && status.pnlPct) || 0)}</b></div>
          <div class="auto-stat"><span>Сделки</span><b>${fmtNum((status && status.trades) || 0, 0)}</b></div>
          <div class="auto-stat"><span>Стратегия</span><b>${esc(strategyTitle(cfg.strategy))}</b></div>
        </div>
      </div>
      <div class="auto-log-head">Журнал автономии</div>
      <div class="auto-log">
        ${rows.length ? rows.map((entry) => `
          <div class="auto-log-row">
            <span class="log-kind ${esc(entry.kind || 'info')}">${esc(LOG_KIND_TITLES[entry.kind] || entry.kind || 'инфо')}</span>
            <span class="log-msg">${esc(entry.message || '')}</span>
            <span class="log-time">${fmtHM(entry.time)}</span>
          </div>`).join('') : '<div class="empty-state small">Журнал пуст — запустите автономию.</div>'}
      </div>`;
  }

  readAutonomyConfig() {
    const form = this.root.querySelector('#autonomyForm');
    const state = this.store.get();
    const data = form ? new FormData(form) : null;
    const number = (name, fallback) => {
      const raw = data ? Number(data.get(name)) : NaN;
      return Number.isFinite(raw) ? raw : fallback;
    };
    return {
      symbol: (data && data.get('symbol')) || state.selected || 'TQBR:SBER',
      periodSec: number('periodSec', state.periodSec || 300),
      strategy: (data && data.get('strategy')) || 'sma_cross',
      params: { fast: 10, slow: 30 },
      speed: clamp(number('speed', 5), 1, 60),
      budget: number('budget', 200000),
      maxPositionPct: number('maxPositionPct', 25),
      stopLossPct: number('stopLossPct', 2),
      takeProfitPct: number('takeProfitPct', 5),
      dailyLossPct: number('dailyLossPct', 3),
      live: false,
    };
  }

  async startAutonomy() {
    const state = this.store.get();
    if (state.autonomy && state.autonomy.running) {
      this.onToast('info', 'Автономия уже запущена');
      return;
    }
    const config = this.readAutonomyConfig();
    try {
      const status = await this.api.autonomy({ action: 'start', config });
      this.store.set({
        autonomy: status,
        autonomyLog: (status && status.log) || [],
      });
      this.setTab('autonomy');
      this.onToast('success', `Автономия запущена: ${config.symbol.split(':').pop()}, ${strategyTitle(config.strategy)}`);
    } catch (error) {
      this.onToast('error', `Не удалось запустить автономию: ${error.message || error}`);
    }
  }

  async stopAutonomy() {
    try {
      const status = await this.api.autonomy({ action: 'stop' });
      this.store.set({ autonomy: status, autonomyLog: (status && status.log) || [] });
      this.onToast('info', 'Автономия остановлена (paper-счёт)');
    } catch (error) {
      this.onToast('error', `Не удалось остановить автономию: ${error.message || error}`);
    }
  }

  instrumentName(symbol) {
    const inst = this.store.get().instruments.find((row) => row.symbol === symbol);
    return inst ? (inst.shortName || inst.name) : (symbol || '').split(':').pop();
  }

  destroy() {
    this.unsubscribe?.();
    this.equityCharts = [];
  }
}

void SOURCE_TITLES;
void fmtDateTime;
void pluralRu;
void timeAgo;
void STRATEGY_TITLES;
