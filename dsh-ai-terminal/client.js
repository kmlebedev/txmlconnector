// Client half of the AI Terminal plugin: a sidebar entry and its main panel.
//
// The panel is host-themed chrome around the embedded terminal SPA: the Host
// half proxies the Go backend under /ai-terminal/ on the Harness origin, so the
// iframe is same-origin and the terminal's REST/SSE traffic stays on the same
// origin. The chrome uses only --dsw-alias-* theme tokens.

window.__ModuleLoader__.load({
  id: '@local/dsh-ai-terminal',
  factory(require) {
    const React = require('react');
    const h = React.createElement;

    /** Dictionary namespace owned by this plugin. */
    const NS = 'aiTerminal';
    /** The id shared by the sidebar entry and the main panel it opens. */
    const PANEL_ID = 'ai-terminal';
    const STATUS_URL = '/ai-terminal-ctl/status';
    const RESTART_URL = '/ai-terminal-ctl/restart';
    const TERMINAL_URL = '/ai-terminal/';
    const PROPOSALS_URL = '/ai-terminal/api/agent/proposals';

    const en = {
      panel: 'AI terminal',
      title: 'AI terminal',
      subtitle: 'MOEX · TRANSAQ · paper account',
      refresh: 'Refresh',
      restart: 'Restart backend',
      open: 'Open in browser',
      starting: 'Starting the terminal backend…',
      connecting: 'Connecting to the terminal…',
      running: 'Running',
      stopped: 'Stopped',
      error: 'The terminal backend is unavailable',
      retry: 'Try again',
      modeOffline: 'Offline demo',
      modeLive: 'Live market (read-only)',
      modeAuto: 'Auto',
      disabled: 'The Harness subprocess service is unavailable; start the terminal manually',
    };
    const ru = {
      panel: 'AI-терминал',
      title: 'AI-терминал',
      subtitle: 'MOEX · TRANSAQ · бумажный счёт',
      refresh: 'Обновить',
      restart: 'Перезапустить',
      open: 'Открыть в браузере',
      starting: 'Запускаю бэкенд терминала…',
      connecting: 'Подключаюсь к терминалу…',
      running: 'Работает',
      stopped: 'Остановлен',
      error: 'Бэкенд терминала недоступен',
      retry: 'Попробовать снова',
      modeOffline: 'Офлайн-демо',
      modeLive: 'Живой рынок (read-only)',
      modeAuto: 'Авто',
      disabled: 'Сервис subprocess недоступен — запустите терминал вручную',
    };
    const zh = {
      panel: 'AI 交易终端',
      title: 'AI 交易终端',
      subtitle: 'MOEX · TRANSAQ · 模拟账户',
      refresh: '刷新',
      restart: '重启后端',
      open: '在浏览器中打开',
      starting: '正在启动终端后端…',
      connecting: '正在连接终端…',
      running: '运行中',
      stopped: '已停止',
      error: '终端后端不可用',
      retry: '重试',
      modeOffline: '离线演示',
      modeLive: '实时行情（只读）',
      modeAuto: '自动',
      disabled: 'Harness 子进程服务不可用，请手动启动终端',
    };

    const token = (name) => `var(${name})`;
    const COLORS = {
      base: token('--dsw-alias-bg-base'),
      layer: token('--dsw-alias-bg-layer-1'),
      border: token('--dsw-alias-border-l1'),
      text: token('--dsw-alias-label-primary'),
      secondary: token('--dsw-alias-label-secondary'),
      accent: token('--dsw-alias-brand-primary'),
      success: token('--dsw-alias-state-success-primary'),
      error: token('--dsw-alias-state-error-primary'),
      idle: token('--dsw-alias-state-idle-primary'),
    };

    const styles = {
      root: {
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minHeight: 0,
        background: COLORS.base,
        color: COLORS.text,
        font: 'inherit',
      },
      bar: {
        display: 'flex',
        alignItems: 'center',
        gap: '10px',
        minHeight: '44px',
        padding: '0 12px',
        borderBottom: `1px solid ${COLORS.border}`,
        background: COLORS.layer,
      },
      mark: {
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: '26px',
        height: '26px',
        borderRadius: '7px',
        background: '#23262b',
        color: '#ffcf33',
        flex: 'none',
      },
      titles: { display: 'flex', flexDirection: 'column', minWidth: 0 },
      title: { fontSize: '13px', fontWeight: 600, lineHeight: 1.25 },
      subtitle: {
        fontSize: '11px',
        color: COLORS.secondary,
        lineHeight: 1.3,
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
      },
      dot: (color) => ({ width: '7px', height: '7px', borderRadius: '50%', background: color, flex: 'none' }),
      statusText: { fontSize: '12px', color: COLORS.secondary, whiteSpace: 'nowrap' },
      spacer: { marginLeft: 'auto' },
      actions: { display: 'flex', alignItems: 'center', gap: '6px' },
      button: (primary) => ({
        appearance: 'none',
        border: `1px solid ${primary ? 'transparent' : COLORS.border}`,
        background: primary ? COLORS.accent : 'transparent',
        color: primary ? '#1b1f24' : COLORS.text,
        borderRadius: '7px',
        font: 'inherit',
        fontSize: '12px',
        lineHeight: '26px',
        padding: '0 10px',
        cursor: 'pointer',
      }),
      link: {
        border: `1px solid ${COLORS.border}`,
        borderRadius: '7px',
        color: COLORS.text,
        fontSize: '12px',
        lineHeight: '26px',
        padding: '0 10px',
        textDecoration: 'none',
        whiteSpace: 'nowrap',
      },
      frame: { flex: 1, width: '100%', border: 0, display: 'block', background: '#fff', minHeight: 0 },
      center: {
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '10px',
        padding: '32px',
        textAlign: 'center',
      },
      headline: { fontSize: '14px', fontWeight: 600 },
      message: { fontSize: '12.5px', color: COLORS.secondary, maxWidth: '560px', lineHeight: 1.5 },
    };

    function TerminalMark({ size = 16 }) {
      return h('svg', {
        width: size,
        height: size,
        viewBox: '0 0 24 24',
        fill: 'none',
        'aria-hidden': true,
        style: { display: 'block' },
      },
        h('path', { d: 'M7 4v4M7 12v3M7 18v2', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round' }),
        h('rect', { x: 4.5, y: 9, width: 5, height: 3.6, rx: 1, fill: 'currentColor' }),
        h('path', { d: 'M12 3v5M12 12v3M12 18v3', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round' }),
        h('rect', { x: 9.5, y: 9, width: 5, height: 3.6, rx: 1, fill: 'currentColor' }),
        h('path', { d: 'M17 5v3M17 12v3M17 18v2', stroke: 'currentColor', strokeWidth: 1.6, strokeLinecap: 'round' }),
        h('rect', { x: 14.5, y: 9, width: 5, height: 3.6, rx: 1, fill: 'currentColor' }),
        h('path', { d: 'M3.5 20h17', stroke: 'currentColor', strokeWidth: 1.2, strokeLinecap: 'round', opacity: 0.55 }),
      );
    }

    /** The sidebar glyph; the shell hands over the requested edge and selection. */
    function TerminalPanelIcon({ size = 20 }) {
      return h('svg', {
        width: size,
        height: size,
        viewBox: '0 0 64 64',
        fill: 'none',
        'aria-hidden': true,
        style: { display: 'block' },
      },
        h('path', { d: 'M17 16v10M17 34v6M17 46v4', stroke: 'currentColor', strokeWidth: 3, strokeLinecap: 'round', opacity: 0.75 }),
        h('rect', { x: 12, y: 26, width: 10, height: 8, rx: 2, fill: 'currentColor' }),
        h('path', { d: 'M32 10v10M32 30v6M32 42v8', stroke: 'currentColor', strokeWidth: 3, strokeLinecap: 'round', opacity: 0.75 }),
        h('rect', { x: 27, y: 20, width: 10, height: 10, rx: 2, fill: 'currentColor' }),
        h('path', { d: 'M47 18v8M47 34v6M47 46v4', stroke: 'currentColor', strokeWidth: 3, strokeLinecap: 'round', opacity: 0.75 }),
        h('rect', { x: 42, y: 26, width: 10, height: 8, rx: 2, fill: 'currentColor' }),
      );
    }

    function text(t, key, fallback) {
      if (typeof t !== 'function') return fallback;
      try {
        return t(key) || fallback;
      } catch {
        return fallback;
      }
    }

    function modeLabel(t, mode) {
      if (mode === 'live') return text(t, 'modeLive', 'Live');
      if (mode === 'auto') return text(t, 'modeAuto', 'Auto');
      if (mode === 'offline') return text(t, 'modeOffline', 'Offline');
      return '';
    }

    function phaseView(t, status) {
      const phase = status && status.phase ? status.phase : 'loading';
      if (phase === 'running') {
        return { color: COLORS.success, label: typeof status.message === 'string' && status.message ? status.message : text(t, 'running', 'Running') };
      }
      if (phase === 'error') {
        return { color: COLORS.error, label: typeof status.message === 'string' && status.message ? status.message : text(t, 'error', 'Error') };
      }
      if (phase === 'stopped') {
        return { color: COLORS.idle, label: typeof status.message === 'string' && status.message ? status.message : text(t, 'stopped', 'Stopped') };
      }
      return { color: COLORS.idle, label: text(t, 'starting', 'Starting…') };
    }

    function Button({ children, onClick, primary, disabled }) {
      return h('button', {
        type: 'button',
        onClick,
        disabled: Boolean(disabled),
        style: { ...styles.button(primary), opacity: disabled ? 0.55 : 1, cursor: disabled ? 'default' : 'pointer' },
      }, children);
    }


    // --- Harness tool cards (plan Task 9) ---------------------------------
    //
    // One tool.call.toolview component per bridged Go tool. The structured data
    // comes from the Host bridge as output.presentationMeta, which the Harness
    // pipeline persists in tool/result.meta and the Chat view hands over as
    // block.meta.

    const CARD = {
      root: {
        border: `1px solid ${COLORS.border}`,
        borderRadius: '8px',
        padding: '10px 12px',
        margin: '6px 0',
        background: COLORS.layer,
        color: COLORS.text,
        fontSize: '12px',
        maxWidth: '620px',
      },
      head: { display: 'flex', alignItems: 'baseline', gap: '8px', marginBottom: '6px' },
      title: { fontSize: '12.5px', fontWeight: 600 },
      sub: { color: COLORS.secondary, fontSize: '11px', marginLeft: 'auto', whiteSpace: 'nowrap' },
      grid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(108px, 1fr))', gap: '6px 12px' },
      cell: { display: 'flex', flexDirection: 'column', gap: '1px' },
      label: { color: COLORS.secondary, fontSize: '10.5px' },
      value: { fontSize: '12.5px', fontWeight: 600, fontVariantNumeric: 'tabular-nums' },
      actions: { display: 'flex', gap: '6px', marginTop: '8px' },
      note: { color: COLORS.secondary, fontSize: '11px', marginTop: '6px', lineHeight: 1.4 },
      error: { color: COLORS.error, fontSize: '11px', marginTop: '4px' },
    };

    /** Format a number the Russian way: comma separator, fixed digits. */
    function cardNumber(value, digits = 2) {
      if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
      return value.toFixed(digits).replace('.', ',');
    }

    /** One labelled value cell of a tool card. */
    function cardCell(label, value) {
      return h('div', { style: CARD.cell },
        h('span', { style: CARD.label }, label),
        h('span', { style: CARD.value }, value));
    }

    /** Polyline path of an equity curve (values only; time axis is uniform). */
    function equityPolyline(equity) {
      const values = equity
        .map((point) => (point && typeof point.value === 'number' ? point.value : Number(point)))
        .filter((value) => Number.isFinite(value));
      if (values.length < 2) return '';
      const min = Math.min(...values);
      const max = Math.max(...values);
      const span = max - min || 1;
      return values.map((value, index) => {
        const x = (index / (values.length - 1)) * 200;
        const y = 38 - ((value - min) / span) * 36;
        return x.toFixed(1) + ',' + y.toFixed(1);
      }).join(' ');
    }

    /** Wait frame of a tool card before its result arrives. */
    function toolCardWaiting(label) {
      return h('div', { style: CARD.root }, h('span', { style: CARD.sub }, label));
    }

    /** GET /api/backtest reply card. */
    function BacktestToolCard({ phase, block }) {
      const card = useBotCardState();
      const meta = block && block.meta && block.meta.kind === 'backtest' ? block.meta : null;
      if (phase !== 'result' || meta === null) return toolCardWaiting('Бэктест выполняется…');
      const metrics = meta.metrics || {};
      const points = equityPolyline(Array.isArray(meta.equity) ? meta.equity : []);
      const canBot = canCreateBot(meta);
      return h('div', { style: CARD.root },
        h('div', { style: CARD.head },
          h('span', { style: CARD.title }, 'Бэктест · ' + (meta.strategy || '—')),
          h('span', { style: CARD.sub }, meta.symbol || '')),
        h('div', { style: CARD.grid },
          cardCell('Доходность', cardNumber(metrics.totalReturnPct) + '%'),
          cardCell('Просадка', cardNumber(metrics.maxDrawdownPct) + '%'),
          cardCell('Сделок', String(metrics.trades ?? '—')),
          cardCell('Sharpe', cardNumber(metrics.sharpe))),
        points
          ? h('svg', {
            viewBox: '0 0 200 40',
            width: '100%',
            height: 40,
            preserveAspectRatio: 'none',
            role: 'img',
            'aria-label': 'Кривая доходности бэктеста',
            style: { display: 'block', marginTop: '8px' },
          }, h('polyline', { points, fill: 'none', stroke: COLORS.accent, strokeWidth: 1.6 }))
          : null,
        Array.isArray(meta.warnings) && meta.warnings.length > 0
          ? h('div', { style: CARD.note }, meta.warnings.join(' · '))
          : null,
        h('div', { style: CARD.note }, canBot
          ? 'Бот-контроль: создать бота из этих правил и проверить/dry-run/перевести на бумагу (подтверждение человека обязательно).'
          : 'Кнопки ботов доступны для бэктеста правил (rules.Spec): каталоговые стратегии в боты не переводятся.'),
        botActionRow(card, meta, true),
        ...botCardFeedback(card));
    }

    /** GET /api/agent/tools/get_quote reply card. */
    function QuoteToolCard({ phase, block }) {
      const meta = block && block.meta && block.meta.kind === 'quote' ? block.meta : null;
      if (phase !== 'result' || meta === null) return toolCardWaiting('Запрашиваю котировку…');
      const change = typeof meta.changePct === 'number' ? meta.changePct : null;
      return h('div', { style: CARD.root },
        h('div', { style: CARD.head },
          h('span', { style: CARD.title }, 'Котировка · ' + (meta.symbol || '—')),
          h('span', { style: CARD.sub }, change === null ? '' : (change >= 0 ? '+' : '') + cardNumber(change) + '%')),
        h('div', { style: CARD.grid },
          cardCell('Последняя', cardNumber(meta.last)),
          cardCell('Bid', cardNumber(meta.bid)),
          cardCell('Ask', cardNumber(meta.ask))));
    }

    /** POST /api/agent/proposals/{id}/approve|reject. */
    async function proposalAction(id, action) {
      const response = await fetch(PROPOSALS_URL + '/' + encodeURIComponent(id) + '/' + action, {
        method: 'POST',
        headers: { 'content-type': 'application/json', Accept: 'application/json' },
        body: '{}',
      });
      const data = await response.json().catch(() => null);
      if (!response.ok) {
        const message = data && data.error && data.error.message ? data.error.message : 'HTTP ' + response.status;
        throw new Error(message);
      }
      return data;
    }

    /** A pending mutating tool proposal with approve/reject controls. */
    function ProposalToolCard({ phase, block }) {
      const proposal = block && block.meta && block.meta.kind === 'proposal' ? block.meta.proposal : null;
      const [status, setStatus] = React.useState(proposal ? proposal.status : 'pending');
      const [busy, setBusy] = React.useState(false);
      const [error, setError] = React.useState('');
      if (phase !== 'result' || proposal === null) return toolCardWaiting('Готовлю предложение…');
      const decide = async (action) => {
        setBusy(true);
        setError('');
        try {
          const data = await proposalAction(proposal.id, action);
          const next = data && data.proposal && data.proposal.status ? data.proposal.status : (action === 'approve' ? 'executed' : 'rejected');
          setStatus(next);
        } catch (failure) {
          setError(failure instanceof Error ? failure.message : String(failure));
        } finally {
          setBusy(false);
        }
      };
      const pending = status === 'pending';
      return h('div', { style: CARD.root },
        h('div', { style: CARD.head },
          h('span', { style: CARD.title }, 'Требуется подтверждение'),
          h('span', { style: CARD.sub }, String(status))),
        h('div', { style: CARD.note }, proposal.summary || String(proposal.tool || '')),
        h('div', { style: CARD.note }, 'Действие не выполнено, пока вы его не подтвердите.'),
        pending
          ? h('div', { style: CARD.actions },
            h(Button, { onClick: () => decide('approve'), primary: true, disabled: busy }, 'Подтвердить'),
            h(Button, { onClick: () => decide('reject'), disabled: busy }, 'Отклонить'))
          : null,
        error ? h('div', { style: CARD.error }, error) : null);
    }


    // --- Harness client surfaces (plan Task 10) ---------------------------
    //
    // A right-panel "AI terminal" tab (order book + positions), a session header
    // action that inserts the visible chart as a ScreenContext JSON quote, and a
    // selected-text interaction "Спросить о выделенном". The exact slots follow
    // docs/ai-terminal/dsh-integration.md §3.3-3.5; the insertQuote path is the
    // standard inputActions prop and falls back to the clipboard (status I).

    const TERMINAL_TAB_ID = 'ai-terminal/terminal';
    const TERMINAL_TAB_KIND = 'ai-terminal';
    const SELECTION_INTERACTION_ID = 'ai-terminal-ask-selection';
    const SCREEN_POLL_MS = 2_000;
    // Autonomous nightly research (plan Task 21): right-panel control surface.
    const AUTONOMY_TAB_ID = 'ai-terminal/autonomy';
    const AUTONOMY_TAB_KIND = 'ai-terminal-autonomy';
    const AUTONOMY_STATUS_URL = '/ai-terminal-ctl/autonomy/status';
    const AUTONOMY_RUN_URL = '/ai-terminal-ctl/autonomy/run';
    const AUTONOMY_STOP_URL = '/ai-terminal-ctl/autonomy/stop';

    /** Last known screen context of the embedded terminal SPA. */
    let screenContext = { source: 'ai-terminal', url: TERMINAL_URL, activeTab: 'chart', symbol: '', periodSec: 0 };
    /** The conversation input actions captured from a standard slot prop. */
    let conversationInputActions = null;
    /** Selected text captured by the text interaction. */
    let selectedTextForSurface = '';

    /** Capture the injected inputActions standard prop. */
    function rememberInputActions(props) {
      if (props && props.inputActions && typeof props.inputActions.insertQuote === 'function') {
        conversationInputActions = props.inputActions;
      }
      return conversationInputActions;
    }

    /** Insert a quote into the conversation composer (clipboard fallback). */
    function insertQuote(text) {
      const actions = conversationInputActions;
      if (actions && typeof actions.insertQuote === 'function') {
        actions.insertQuote(text);
        return true;
      }
      if (typeof navigator !== 'undefined' && navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        void navigator.clipboard.writeText(text).catch(() => {});
      }
      return false;
    }

    /** Build the ScreenContext JSON quote of the visible chart (§9.6). */
    function buildScreenContextQuote(context) {
      const source = context && typeof context === 'object' ? context : {};
      return JSON.stringify({
        source: 'ai-terminal',
        activeTab: typeof source.activeTab === 'string' && source.activeTab !== '' ? source.activeTab : 'chart',
        symbol: typeof source.symbol === 'string' ? source.symbol : '',
        periodSec: Number.isFinite(source.periodSec) ? source.periodSec : 0,
        from: typeof source.from === 'string' ? source.from : '',
        to: typeof source.to === 'string' ? source.to : '',
        selection: source.selection && typeof source.selection === 'object' ? source.selection : null,
      });
    }

    /** Build the quote of a selected chat text. */
    function buildSelectionQuote(text) {
      const value = typeof text === 'string' ? text.trim() : '';
      return JSON.stringify({
        source: 'ai-terminal',
        activeTab: 'chat',
        selection: { text: value.slice(0, 2000) },
      });
    }

    /** Read the visible terminal screen from the same-origin iframe URL. */
    function refreshScreenContext(frame) {
      try {
        const href = frame && frame.contentWindow && frame.contentWindow.location ? frame.contentWindow.location.href : '';
        if (!href || href === 'about:blank') return;
        const url = new URL(href);
        screenContext = {
          ...screenContext,
          url: href,
          symbol: url.searchParams.get('symbol') || screenContext.symbol,
          periodSec: Number(url.searchParams.get('period')) || screenContext.periodSec,
          activeTab: url.searchParams.get('tab') || screenContext.activeTab,
        };
      } catch {
        // A cross-origin frame stays at the last known context.
      }
    }

    /** Compact order-book rows for the right-panel tab. */
    function bookRows(book) {
      const bids = book && Array.isArray(book.bids) ? book.bids.slice(0, 5) : [];
      const asks = book && Array.isArray(book.asks) ? book.asks.slice(0, 5) : [];
      return { bids, asks };
    }

    /** Right-panel "AI terminal" tab: the terminal order book and positions. */
    function TerminalPanelTabBody() {
      const [state, setState] = React.useState({ loading: true, symbol: '', book: null, positions: [], error: '' });
      React.useEffect(() => {
        let live = true;
        const load = async () => {
          try {
            const portfolio = await fetch('/ai-terminal/api/portfolio').then((response) => response.json());
            const positions = portfolio && Array.isArray(portfolio.positions) ? portfolio.positions : [];
            const symbol = positions.length > 0 && positions[0].symbol ? positions[0].symbol : '';
            const book = symbol
              ? await fetch('/ai-terminal/api/instruments/' + encodeURIComponent(symbol) + '/orderbook').then((response) => response.json())
              : null;
            if (live) setState({ loading: false, symbol, book, positions, error: '' });
          } catch {
            if (live) setState({ loading: false, symbol: '', book: null, positions: [], error: 'Терминал недоступен' });
          }
        };
        void load();
        return () => { live = false; };
      }, []);
      if (state.loading) return h('div', { style: CARD.note }, 'Загрузка терминала…');
      if (state.error) return h('div', { style: CARD.error }, state.error);
      const rows = bookRows(state.book);
      const levels = rows.asks.slice().reverse().concat(rows.bids);
      return h('div', { style: { display: 'flex', flexDirection: 'column', gap: '8px', padding: '10px 12px', fontSize: '12px' } },
        h('div', { style: CARD.title }, 'Стакан · ' + (state.symbol || '—')),
        levels.length === 0
          ? h('div', { style: CARD.note }, 'Нет данных стакана')
          : h('div', { style: { display: 'flex', flexDirection: 'column', gap: '2px' } },
            levels.map((level, index) => h('div', {
              key: 'level-' + index,
              style: { display: 'flex', justifyContent: 'space-between', fontVariantNumeric: 'tabular-nums' },
            },
              h('span', null, cardNumber(level.price)),
              h('span', { style: { color: COLORS.secondary } }, String(level.qty ?? ''))))),
        h('div', { style: CARD.title }, 'Позиции'),
        state.positions.length === 0
          ? h('div', { style: CARD.note }, 'Позиций нет')
          : h('div', { style: { display: 'flex', flexDirection: 'column', gap: '2px' } },
            state.positions.map((position, index) => h('div', {
              key: 'position-' + index,
              style: { display: 'flex', justifyContent: 'space-between' },
            },
              h('span', null, String(position.symbol || '')),
              h('span', { style: { color: COLORS.secondary } }, String(position.qty ?? ''))))));
    }

    /** Session header action: insert the visible chart into the chat input. */
    function SendChartQuoteAction(props) {
      rememberInputActions(props);
      const send = () => {
        refreshScreenContext(null);
        insertQuote(buildScreenContextQuote(screenContext));
      };
      return h('button', {
        type: 'button',
        style: styles.button(false),
        title: 'Отправить график в чат',
        'aria-label': 'Отправить график в чат',
        onClick: send,
      }, 'В чат');
    }

    /** Selected-text surface of the UiConversation interaction. */
    function AskSelectionAction(props) {
      rememberInputActions(props);
      const text = selectedTextForSurface !== ''
        ? selectedTextForSurface
        : (props && typeof props.readText === 'function' ? props.readText() : '');
      const ask = () => {
        if (insertQuote(buildSelectionQuote(text)) && props && typeof props.dismiss === 'function') props.dismiss();
      };
      return h('button', {
        type: 'button',
        style: styles.button(false),
        onClick: ask,
      }, 'Спросить о выделенном');
    }


    /** Fetch one autonomy control endpoint, keeping the status payload on errors. */
    async function fetchAutonomy(url, init) {
      const response = await fetch(url, {
        cache: 'no-store',
        headers: { Accept: 'application/json' },
        ...(init || {}),
      });
      let data = null;
      try {
        data = await response.json();
      } catch {
        data = null;
      }
      if (!response.ok) {
        const message = data && data.error && data.error.message ? data.error.message : 'HTTP ' + response.status;
        const error = new Error(message);
        error.status = response.status;
        error.payload = data;
        throw error;
      }
      return data;
    }

    /** Human label of one autonomy run state. */
    function autonomyRunLabel(status) {
      if (!status) return '—';
      if (status.running) return 'выполняется';
      if (status.stopReason === 'cancelled') return 'отменено (' + String(status.reason || '') + ')';
      if (status.stopReason === 'error') return 'ошибка';
      if (status.lastFinishedAt) return 'завершено';
      return 'ещё не запускалось';
    }

    /** Right-panel "Автономия" tab: last run, token budget and kill switch (plan Task 21). */
    function AutonomyPanelTabBody() {
      const [state, setState] = React.useState({ loading: true, busy: '', status: null, error: '' });
      const refresh = React.useCallback(async () => {
        try {
          const status = await fetchAutonomy(AUTONOMY_STATUS_URL);
          setState((current) => ({ ...current, loading: false, status, error: '' }));
        } catch (error) {
          setState((current) => ({ ...current, loading: false, error: error instanceof Error ? error.message : String(error) }));
        }
      }, []);
      React.useEffect(() => {
        let alive = true;
        void refresh();
        const timer = setInterval(() => {
          if (alive) void refresh();
        }, 3_000);
        return () => {
          alive = false;
          clearInterval(timer);
        };
      }, [refresh]);
      const act = React.useCallback(async (url, busy) => {
        setState((current) => ({ ...current, busy, error: '' }));
        try {
          await fetchAutonomy(url, { method: 'POST' });
        } catch (error) {
          setState((current) => ({ ...current, error: error instanceof Error ? error.message : String(error) }));
        } finally {
          setState((current) => ({ ...current, busy: '' }));
          await refresh();
        }
      }, [refresh]);
      const status = state.status;
      const usage = status && status.usage ? status.usage : { total: 0 };
      const budget = status && status.budget ? status.budget : { tokenBudget: 0 };
      return h('div', { style: { display: 'flex', flexDirection: 'column', gap: '6px', padding: '10px 12px', fontSize: '12px' } },
        h('div', { style: CARD.title }, 'Автономия'),
        h('div', { style: CARD.note }, state.loading ? 'Загрузка…' : (status && status.enabled ? 'Расписание включено' : 'Расписание выключено (autonomy.enabled: false)')),
        h('div', { style: CARD.note }, 'Состояние: ' + autonomyRunLabel(status)),
        h('div', { style: CARD.note }, 'Токены: ' + String(usage.total) + ' / ' + String(budget.tokenBudget) + ' · лимит ' + String(budget.maxWallMinutes) + ' мин'),
        status && status.scheduleId ? h('div', { style: CARD.note }, 'Расписание: ' + String(status.scheduleId)) : null,
        status && status.lastFinishedAt ? h('div', { style: CARD.note }, 'Последний запуск: ' + new Date(status.lastFinishedAt).toLocaleString('ru-RU')) : null,
        h('div', { style: { display: 'flex', gap: '6px', marginTop: '4px' } },
          h('button', {
            type: 'button',
            style: styles.button(true),
            disabled: state.busy !== '' || (status !== null && status.running),
            onClick: () => { void act(AUTONOMY_RUN_URL, 'run'); },
          }, 'Запустить сейчас'),
          h('button', {
            type: 'button',
            style: styles.button(false),
            disabled: state.busy !== '',
            onClick: () => { void act(AUTONOMY_STOP_URL, 'stop'); },
          }, 'Стоп')),
        state.error !== '' ? h('div', { style: CARD.error }, state.error) : null);
    }


    // --- Bot control room (plan Task 26) ---------------------------------
    //
    // Cards and always-on surfaces over the /api/bots REST and the SSE "bot"
    // events proxied by the plugin. The Host relay (bot-relay.js) injects the
    // significant events into the session agent; the Client renders the live
    // card at the turn tail, the mini card in the input dock, the kill switch
    // in the session header and the "Боты" right-panel tab.

    const BOTS_URL = '/ai-terminal/api/bots';
    const BOTS_STOP_ALL_URL = '/ai-terminal/api/bots/stop-all';
    const BOT_EVENTS_URL = '/ai-terminal/api/events';

    /** One same-origin JSON request to the terminal backend through the proxy. */
    async function terminalJSON(path, init) {
      const options = init || {};
      const headers = { Accept: 'application/json', ...(options.headers || {}) };
      if (options.body !== undefined) headers['content-type'] = 'application/json';
      const response = await fetch(path, { cache: 'no-store', ...options, headers });
      let data = null;
      try {
        data = await response.json();
      } catch {
        data = null;
      }
      if (!response.ok) {
        const message = data && data.error && data.error.message ? data.error.message : 'HTTP ' + response.status;
        throw new Error(message);
      }
      return data;
    }

    /** Human stage label of a bot (mirrors bot-ui.js on the Host half). */
    function botStageText(stage) {
      if (stage === 'check') return 'проверка';
      if (stage === 'dry_run') return 'dry-run';
      if (stage === 'paper') return 'бумага';
      if (stage === 'stopped') return 'остановлен';
      return String(stage || '—');
    }

    /** Human journal-kind label of a bot. */
    function botKindText(kind) {
      if (kind === 'closed_bar') return 'свеча';
      if (kind === 'signal') return 'сигнал';
      if (kind === 'risk_check') return 'риск-контроль';
      if (kind === 'order') return 'заявка';
      if (kind === 'fill') return 'исполнение';
      if (kind === 'reconnect') return 'реконнект';
      if (kind === 'error') return 'ошибка';
      if (kind === 'stage') return 'стадия';
      return String(kind || '—');
    }

    /** One-line journal entry. */
    function botJournalLine(entry) {
      if (!entry || typeof entry !== 'object') return '';
      const parts = [botKindText(entry.kind)];
      if (typeof entry.close === 'number') parts.push('close ' + cardNumber(entry.close));
      if (entry.indicators && typeof entry.indicators === 'object') {
        for (const [name, value] of Object.entries(entry.indicators)) {
          if (typeof value === 'number' && Number.isFinite(value)) parts.push(name + '=' + cardNumber(value));
        }
      }
      if (typeof entry.signal === 'string' && entry.signal !== 'none') parts.push('сигнал ' + entry.signal);
      if (typeof entry.message === 'string' && entry.message !== '') parts.push(entry.message);
      return parts.join(' · ');
    }

    /** Whether the "stop all" kill switch must be visible. */
    function botsRunning(bots) {
      return Array.isArray(bots) && bots.some((bot) => bot && typeof bot.stage === 'string' && bot.stage !== 'stopped');
    }

    /** Rule bots are created from a rules.Spec backtest: {symbol, periodSec, rules}. */
    function canCreateBot(meta) {
      return meta && typeof meta === 'object' && meta.rules && typeof meta.rules === 'object'
        && typeof meta.symbol === 'string' && meta.symbol !== '';
    }

    /** POST /api/bots from one rules backtest card. */
    async function createRuleBot(meta, qty) {
      const data = await terminalJSON(BOTS_URL, {
        method: 'POST',
        body: JSON.stringify({
          symbol: meta.symbol,
          periodSec: Number.isFinite(meta.periodSec) && meta.periodSec > 0 ? meta.periodSec : 86400,
          rules: meta.rules,
          qty: Number.isFinite(qty) && qty > 0 ? qty : 1,
        }),
      });
      const bot = data && data.bot ? data.bot : null;
      if (bot === null || typeof bot.id !== 'string' || bot.id === '') throw new Error('бэкенд не вернул бота');
      return bot;
    }

    /** POST one bot stage action; returns the refreshed bot payload. */
    async function botAction(botId, action, body) {
      return terminalJSON(BOTS_URL + '/' + encodeURIComponent(botId) + '/' + action, {
        method: 'POST',
        body: JSON.stringify(body || {}),
      });
    }

    /** Bot card actions shared by the backtest and the bot tool cards. */
    function useBotCardState() {
      const [bot, setBot] = React.useState(null);
      const [status, setStatus] = React.useState('');
      const [proposalId, setProposalId] = React.useState('');
      const [busy, setBusy] = React.useState(false);
      const [error, setError] = React.useState('');
      return { bot, setBot, status, setStatus, proposalId, setProposalId, busy, setBusy, error, setError };
    }

    /** Run one card action, creating the rule bot first when the card has only a backtest. */
    async function runBotCardAction(card, meta, action) {
      card.setBusy(true);
      card.setError('');
      try {
        let current = card.bot;
        if (current === null || !current.id) {
          if (!canCreateBot(meta)) throw new Error('кнопки доступны для бэктеста с rules.Spec');
          current = await createRuleBot(meta);
          card.setBot(current);
        }
        if (action === 'check') {
          const data = await botAction(current.id, 'check');
          const entry = data && data.entry ? data.entry : null;
          card.setStatus(entry ? 'Проверка: ' + botJournalLine(entry) : 'Проверка выполнена');
        } else if (action === 'dry_run') {
          await botAction(current.id, 'start', { stage: 'dry_run' });
          card.setStatus('Бот запущен в dry-run');
        } else if (action === 'paper') {
          const data = await botAction(current.id, 'promote', { stage: 'paper' });
          const id = data && typeof data.proposalId === 'string' ? data.proposalId : '';
          if (id === '') throw new Error('бэкенд не вернул proposalId');
          card.setProposalId(id);
          card.setStatus('Перевод на бумагу ждёт подтверждения');
        } else if (action === 'stop') {
          await botAction(current.id, 'stop');
          card.setStatus('Бот остановлен');
        }
      } catch (failure) {
        card.setError(failure instanceof Error ? failure.message : String(failure));
      } finally {
        card.setBusy(false);
      }
    }

    /** Approve/reject buttons of the paper-stage proposal. */
    async function decideBotProposal(card, action) {
      card.setBusy(true);
      card.setError('');
      try {
        await proposalAction(card.proposalId, action);
        card.setStatus(action === 'approve' ? 'Подтверждено: бот переведён на бумагу' : 'Перевод отклонён');
        card.setProposalId('');
      } catch (failure) {
        card.setError(failure instanceof Error ? failure.message : String(failure));
      } finally {
        card.setBusy(false);
      }
    }

    /** Action rows of one bot card. */
    function botActionRow(card, meta, compact) {
      const disabled = card.busy;
      const canBot = canCreateBot(meta) || (card.bot !== null && card.bot.id);
      return h('div', { style: CARD.actions },
        h(Button, { onClick: () => { void runBotCardAction(card, meta, 'check'); }, disabled: disabled || !canBot }, 'Проверить'),
        h(Button, { onClick: () => { void runBotCardAction(card, meta, 'dry_run'); }, primary: true, disabled: disabled || !canBot }, 'Dry-run'),
        h(Button, { onClick: () => { void runBotCardAction(card, meta, 'paper'); }, disabled: disabled || !canBot }, 'На бумагу'),
        compact ? null : h(Button, { onClick: () => { void runBotCardAction(card, meta, 'stop'); }, disabled: disabled || !canBot }, 'Стоп'));
    }

    /** Status/error/proposal confirmation lines under a bot card. */
    function botCardFeedback(card) {
      return [
        card.status !== '' ? h('div', { key: 'status', style: CARD.note }, card.status) : null,
        card.error !== '' ? h('div', { key: 'error', style: CARD.error }, card.error) : null,
        card.proposalId !== ''
          ? h('div', { key: 'proposal', style: CARD.actions },
            h(Button, { onClick: () => { void decideBotProposal(card, 'approve'); }, primary: true, disabled: card.busy }, 'Подтвердить'),
            h(Button, { onClick: () => { void decideBotProposal(card, 'reject'); }, disabled: card.busy }, 'Отклонить'))
          : null,
      ];
    }

    /** Chat card of one terminal_bot_* tool reply (meta built by bot-ui.js). */
    function BotToolCard({ phase, block }) {
      const card = useBotCardState();
      const meta = block && block.meta ? block.meta : null;
      if (phase !== 'result' || meta === null) return toolCardWaiting('Бот обновляется…');
      if (meta.kind === 'bots') {
        const list = Array.isArray(meta.bots) ? meta.bots : [];
        return h('div', { style: CARD.root },
          h('div', { style: CARD.head }, h('span', { style: CARD.title }, 'Боты'), h('span', { style: CARD.sub }, String(list.length))),
          list.length === 0
            ? h('div', { style: CARD.note }, 'Ботов нет')
            : h('div', { style: { display: 'flex', flexDirection: 'column', gap: '4px' } },
              list.map((bot) => h('div', { key: bot.id, style: { display: 'flex', gap: '8px', justifyContent: 'space-between' } },
                h('span', null, String(bot.symbol || bot.id)),
                h('span', { style: CARD.label }, botStageText(bot.stage) + ' · qty ' + String(bot.qty ?? '—'))))));
      }
      const bot = meta.id ? meta : null;
      if (bot === null) return toolCardWaiting('Бот обновляется…');
      const entries = Array.isArray(meta.entries) ? meta.entries : [];
      return h('div', { style: CARD.root },
        h('div', { style: CARD.head },
          h('span', { style: CARD.title }, 'Бот · ' + (bot.symbol || bot.id)),
          h('span', { style: CARD.sub }, botStageText(bot.stage))),
        h('div', { style: CARD.grid },
          h('div', { style: CARD.cell }, h('span', { style: CARD.label }, 'ID'), h('span', { style: CARD.value }, bot.id)),
          h('div', { style: CARD.cell }, h('span', { style: CARD.label }, 'Qty'), h('span', { style: CARD.value }, String(bot.qty ?? '—'))),
          h('div', { style: CARD.cell }, h('span', { style: CARD.label }, 'Период, с'), h('span', { style: CARD.value }, String(bot.periodSec ?? '—')))),
        meta.last ? h('div', { style: CARD.note }, 'Последняя запись: ' + botJournalLine(meta.last)) : null,
        entries.length > 0
          ? h('div', { style: CARD.note }, 'Журнал: ' + entries.slice(-5).map(botJournalLine).join(' | '))
          : null,
        botActionRow(card, meta, false),
        ...botCardFeedback(card));
    }

    /** Collect one bot's journal tail for the live card. */
    async function fetchBotJournal(botId, limit) {
      const data = await terminalJSON(BOTS_URL + '/' + encodeURIComponent(botId) + '/journal?after=0&limit=' + String(limit || 50));
      return data && Array.isArray(data.entries) ? data.entries : [];
    }

    /** Sparkline of the last closed-bar closes. */
    function closesPolyline(entries) {
      const closes = entries
        .filter((entry) => entry && entry.kind === 'closed_bar' && typeof entry.close === 'number')
        .slice(-100)
        .map((entry) => entry.close);
      if (closes.length < 2) return '';
      const min = Math.min(...closes);
      const max = Math.max(...closes);
      const span = max - min || 1;
      return closes.map((close, index) => {
        const x = (index / (closes.length - 1)) * 200;
        const y = 36 - ((close - min) / span) * 32;
        return x.toFixed(1) + ',' + y.toFixed(1);
      }).join(' ');
    }

    /** Live bot card at the conversation turn tail (plan Task 26). */
    function BotTailCard() {
      const [state, setState] = React.useState({ bots: [], entries: {}, error: '', filter: '' });
      const refresh = React.useCallback(async () => {
        try {
          const data = await terminalJSON(BOTS_URL);
          const bots = data && Array.isArray(data.bots) ? data.bots : [];
          const entries = {};
          for (const bot of bots.slice(0, 4)) {
            try {
              entries[bot.id] = await fetchBotJournal(bot.id, 100);
            } catch {
              entries[bot.id] = [];
            }
          }
          setState((current) => ({ ...current, bots, entries, error: '' }));
        } catch (failure) {
          setState((current) => ({ ...current, error: failure instanceof Error ? failure.message : String(failure) }));
        }
      }, []);
      React.useEffect(() => {
        let alive = true;
        void refresh();
        const timer = setInterval(() => { if (alive) void refresh(); }, 5_000);
        let events = null;
        try {
          events = new EventSource(BOT_EVENTS_URL);
          events.onmessage = () => { if (alive) void refresh(); };
        } catch {
          events = null;
        }
        return () => {
          alive = false;
          clearInterval(timer);
          if (events !== null) events.close();
        };
      }, [refresh]);
      if (state.bots.length === 0) return null;
      const filter = state.filter;
      return h('div', { style: { display: 'flex', flexDirection: 'column', gap: '8px' } },
        state.bots.slice(0, 4).map((bot) => {
          const entries = Array.isArray(state.entries[bot.id]) ? state.entries[bot.id] : [];
          const lastClosed = entries.filter((entry) => entry.kind === 'closed_bar').slice(-1)[0] || null;
          const shown = entries.filter((entry) => filter === '' || entry.kind === filter).slice(-50);
          const points = closesPolyline(entries);
          return h('div', { key: bot.id, style: CARD.root },
            h('div', { style: CARD.head },
              h('span', { style: CARD.title }, 'Бот · ' + (bot.symbol || bot.id)),
              h('span', { style: CARD.sub }, botStageText(bot.stage) + ' · qty ' + String(bot.qty ?? '—'))),
            lastClosed
              ? h('div', { style: CARD.note }, 'Последняя закрытая свеча: ' + new Date(lastClosed.time).toLocaleString('ru-RU')
                + (typeof lastClosed.close === 'number' ? ' · close ' + cardNumber(lastClosed.close) : '')
                + (typeof lastClosed.signal === 'string' && lastClosed.signal !== 'none' ? ' · сигнал ' + lastClosed.signal : ''))
              : null,
            points
              ? h('svg', { viewBox: '0 0 200 40', width: '100%', height: 40, preserveAspectRatio: 'none', role: 'img', 'aria-label': 'Мини-график бота', style: { display: 'block', marginTop: '6px' } },
                h('polyline', { points, fill: 'none', stroke: COLORS.accent, strokeWidth: 1.4 }))
              : null,
            h('select', {
              value: filter,
              onChange: (event) => setState((current) => ({ ...current, filter: event.target.value })),
              style: { marginTop: '6px', ...styles.button(false), padding: '2px 6px' },
            },
            h('option', { value: '' }, 'Все записи'),
            ['closed_bar', 'signal', 'risk_check', 'order', 'fill', 'stage', 'reconnect', 'error'].map((kind) => h('option', { key: kind, value: kind }, botKindText(kind)))),
            shown.length === 0
              ? h('div', { style: CARD.note }, 'Журнал пуст')
              : h('div', { style: CARD.note }, shown.slice(-5).map(botJournalLine).join(' | ')));
        }),
        state.error !== '' ? h('div', { style: CARD.error }, state.error) : null);
    }

    /** Mini bot card pinned in the conversation input dock. */
    function BotDockCard() {
      const [bots, setBots] = React.useState([]);
      React.useEffect(() => {
        let alive = true;
        const load = async () => {
          try {
            const data = await terminalJSON(BOTS_URL);
            if (alive) setBots(data && Array.isArray(data.bots) ? data.bots : []);
          } catch {
            if (alive) setBots([]);
          }
        };
        void load();
        const timer = setInterval(() => { if (alive) void load(); }, 5_000);
        return () => { alive = false; clearInterval(timer); };
      }, []);
      if (bots.length === 0) return null;
      const live = bots.filter((bot) => bot.stage !== 'stopped').length;
      return h('div', { style: { display: 'flex', alignItems: 'center', gap: '8px', padding: '4px 8px', fontSize: '11px', color: COLORS.secondary } },
        h('span', null, 'Боты: ' + String(live) + ' активных из ' + String(bots.length)),
        h('span', null, bots.slice(0, 4).map((bot) => (bot.symbol || bot.id) + ' · ' + botStageText(bot.stage)).join('; ')));
    }

    /** Session header kill switch: visible only while a bot is not stopped. */
    function BotKillSwitchAction() {
      const [bots, setBots] = React.useState([]);
      const [busy, setBusy] = React.useState(false);
      const [error, setError] = React.useState('');
      React.useEffect(() => {
        let alive = true;
        const load = async () => {
          try {
            const data = await terminalJSON(BOTS_URL);
            if (alive) setBots(data && Array.isArray(data.bots) ? data.bots : []);
          } catch {
            if (alive) setBots([]);
          }
        };
        void load();
        const timer = setInterval(() => { if (alive) void load(); }, 5_000);
        return () => { alive = false; clearInterval(timer); };
      }, []);
      if (!botsRunning(bots)) return null;
      const stopAll = async () => {
        setBusy(true);
        setError('');
        try {
          await terminalJSON(BOTS_STOP_ALL_URL, { method: 'POST', body: '{}' });
          setBots((current) => current.map((bot) => ({ ...bot, stage: 'stopped' })));
        } catch (failure) {
          setError(failure instanceof Error ? failure.message : String(failure));
        } finally {
          setBusy(false);
        }
      };
      return h('button', {
        type: 'button',
        style: styles.button(false),
        title: error !== '' ? error : 'Остановить все боты',
        onClick: () => { void stopAll(); },
        disabled: busy,
      }, 'Стоп всех ботов');
    }

    /** Right-panel "Боты" tab: the bot list of the terminal. */
    function BotsPanelTabBody() {
      const [state, setState] = React.useState({ loading: true, bots: [], error: '' });
      const refresh = React.useCallback(async () => {
        try {
          const data = await terminalJSON(BOTS_URL);
          setState({ loading: false, bots: data && Array.isArray(data.bots) ? data.bots : [], error: '' });
        } catch (failure) {
          setState((current) => ({ ...current, loading: false, error: failure instanceof Error ? failure.message : String(failure) }));
        }
      }, []);
      React.useEffect(() => {
        let alive = true;
        void refresh();
        const timer = setInterval(() => { if (alive) void refresh(); }, 5_000);
        return () => { alive = false; clearInterval(timer); };
      }, [refresh]);
      return h('div', { style: { display: 'flex', flexDirection: 'column', gap: '6px', padding: '10px 12px', fontSize: '12px' } },
        h('div', { style: CARD.title }, 'Боты'),
        state.loading
          ? h('div', { style: CARD.note }, 'Загрузка…')
          : (state.bots.length === 0
            ? h('div', { style: CARD.note }, 'Ботов нет — создайте бота из карточки бэктеста')
            : h('div', { style: { display: 'flex', flexDirection: 'column', gap: '4px' } },
              state.bots.map((bot) => h('div', { key: bot.id, style: { display: 'flex', justifyContent: 'space-between' } },
                h('span', null, (bot.symbol || bot.id) + ' · ' + botStageText(bot.stage)),
                h('span', { style: CARD.label }, 'qty ' + String(bot.qty ?? '—')))))),
        state.error !== '' ? h('div', { style: CARD.error }, state.error) : null,
        h('div', { style: CARD.actions },
          h(Button, { onClick: () => { void refresh(); } }, 'Обновить')));
    }

    function TerminalPanel({ t }) {
      const frameRef = React.useRef(null);
      React.useEffect(() => {
        const timer = setInterval(() => refreshScreenContext(frameRef.current), SCREEN_POLL_MS);
        return () => clearInterval(timer);
      }, []);
      const [status, setStatus] = React.useState({ phase: 'loading', message: '' });
      const [reloadKey, setReloadKey] = React.useState(0);
      const [busy, setBusy] = React.useState(false);

      const refresh = React.useCallback(async () => {
        try {
          const response = await fetch(STATUS_URL, { cache: 'no-store', headers: { Accept: 'application/json' } });
          if (!response.ok) throw new Error(`HTTP ${response.status}`);
          const data = await response.json();
          setStatus(data);
        } catch (error) {
          setStatus({ phase: 'error', message: error instanceof Error ? error.message : String(error) });
        }
      }, []);

      React.useEffect(() => {
        let alive = true;
        void refresh();
        const timer = setInterval(() => {
          if (alive) void refresh();
        }, 3000);
        return () => {
          alive = false;
          clearInterval(timer);
        };
      }, [refresh]);

      const restart = React.useCallback(async () => {
        setBusy(true);
        setStatus((current) => ({ ...current, phase: 'starting', message: text(t, 'starting', 'Starting…') }));
        try {
          await fetch(RESTART_URL, { method: 'POST', headers: { Accept: 'application/json' } });
        } catch {
          // The status poll reports the real state.
        } finally {
          setBusy(false);
          await refresh();
          setReloadKey((key) => key + 1);
        }
      }, [refresh, t]);

      const phase = phaseView(t, status);
      const running = status && status.phase === 'running';
      const mode = modeLabel(t, status && status.mode);

      return h('div', { style: styles.root },
        h('div', { style: styles.bar },
          h('span', { style: styles.mark }, h(TerminalMark, { size: 16 })),
          h('div', { style: styles.titles },
            h('div', { style: styles.title }, text(t, 'title', 'AI terminal')),
            h('div', { style: styles.subtitle }, [text(t, 'subtitle', 'MOEX · paper account'), mode ? ` · ${mode}` : ''].join(''))),
          h('span', { style: styles.spacer }),
          h('span', { style: styles.dot(phase.color) }),
          h('span', { style: styles.statusText }, phase.label),
          h('div', { style: styles.actions },
            h(Button, { onClick: async () => { setReloadKey((key) => key + 1); await refresh(); }, disabled: busy }, text(t, 'refresh', 'Refresh')),
            h(Button, { onClick: restart, primary: true, disabled: busy || (status && status.reused) }, text(t, 'restart', 'Restart')),
            h('a', { style: styles.link, href: TERMINAL_URL, target: '_blank', rel: 'noreferrer' }, text(t, 'open', 'Open')))),
        running
          ? h('iframe', {
            key: reloadKey,
            ref: frameRef,
            src: TERMINAL_URL,
            title: text(t, 'title', 'AI terminal'),
            style: styles.frame,
            allow: 'clipboard-write',
          })
          : h('div', { style: styles.center },
            h(TerminalMark, { size: 34 }),
            h('div', { style: styles.headline }, status && status.phase === 'error' ? text(t, 'error', 'Unavailable') : text(t, 'connecting', 'Connecting…')),
            h('div', { style: styles.message }, (status && status.message) || text(t, 'starting', 'Starting…')),
            h(Button, { onClick: async () => { await refresh(); }, primary: true }, text(t, 'retry', 'Try again'))));
    }

    function apply(ctx) {
      ctx.effect(() => ctx.locale.register(NS, {
        en,
        ru,
        zh,
        ja: en,
        es: en,
        fr: en,
        de: en,
        pt: en,
      }), 'ai-terminal: dictionaries');
      const t = ctx.locale.bind(NS);
      ctx.slots.inject('main', () => ctx.slots.register({
        name: 'main',
        key: PANEL_ID,
        locale: NS,
        inject: () => ({ t }),
      }, TerminalPanel));
      ctx.slots.inject('sidebar.panellist', () => ctx.slots.register({
        name: 'sidebar.panellist',
        id: PANEL_ID,
        order: 20,
        label: () => t('panel'),
        locale: NS,
      }, TerminalPanelIcon));

      // Chat cards of the bridged terminal tools (plan Task 9); the key is the
      // wire tool name registered by the Host half.
      const toolView = (key, Component) => ctx.slots.inject('tool.call.toolview', () => ctx.slots.register({
        name: 'tool.call.toolview',
        key,
        locale: NS,
      }, Component));
      toolView('terminal_run_backtest', BacktestToolCard);
      toolView('terminal_get_quote', QuoteToolCard);
      // A proposal is the canonical result of a gated mutating tool, so the
      // same card is keyed by those wire names; terminal_proposal is the
      // generic plan key for a proposal surfaced on its own.
      for (const key of ['terminal_proposal', 'terminal_place_order', 'terminal_cancel_order', 'terminal_start_autonomy']) {
        toolView(key, ProposalToolCard);
      }
      // Rule-bot tools (plan Task 26): the card shows the bot stage and offers
      // the same REST actions as the tools (check / dry-run / paper / stop).
      for (const key of [
        'terminal_bot_create_from_backtest',
        'terminal_bot_check',
        'terminal_bot_start',
        'terminal_bot_promote',
        'terminal_bot_stop',
        'terminal_bot_status',
        'terminal_bot_journal',
      ]) {
        toolView(key, BotToolCard);
      }

      // Right-panel "AI terminal" tab (plan Task 10): the type is registered in
      // ctx.sidebarRightTabs, its body through the sidebar.right.pane.tab slot.
      if (ctx.sidebarRightTabs && typeof ctx.sidebarRightTabs.register === 'function') {
        ctx.effect(() => ctx.sidebarRightTabs.register({
          id: TERMINAL_TAB_ID,
          kind: TERMINAL_TAB_KIND,
          title: () => t('panel'),
        }), 'ai-terminal: sidebar tab type');
      }
      ctx.slots.inject('sidebar.right.pane.tab', () => ctx.slots.register({
        name: 'sidebar.right.pane.tab',
        key: TERMINAL_TAB_ID,
        locale: NS,
      }, TerminalPanelTabBody));

      // Right-panel "Автономия" tab (plan Task 21): last run, budget, kill switch.
      if (ctx.sidebarRightTabs && typeof ctx.sidebarRightTabs.register === 'function') {
        ctx.effect(() => ctx.sidebarRightTabs.register({
          id: AUTONOMY_TAB_ID,
          kind: AUTONOMY_TAB_KIND,
          title: () => 'Автономия',
        }), 'ai-terminal: autonomy tab type');
      }
      ctx.slots.inject('sidebar.right.pane.tab', () => ctx.slots.register({
        name: 'sidebar.right.pane.tab',
        key: AUTONOMY_TAB_ID,
        locale: NS,
      }, AutonomyPanelTabBody));



      // Session header action: send the visible chart into the chat input.
      ctx.slots.inject('conversation.session.header.actions', () => ctx.slots.register({
        name: 'conversation.session.header.actions',
        id: 'ai-terminal-send-chart',
        locale: NS,
      }, SendChartQuoteAction));

      // Selected text: "Ask about the selection" through the UiConversation
      // text-interaction registry and its keyed surface slot.
      if (ctx.uiConversation && ctx.uiConversation.interactions && typeof ctx.uiConversation.interactions.register === 'function') {
        ctx.effect(() => ctx.uiConversation.interactions.register({
          id: SELECTION_INTERACTION_ID,
          onInteraction(gesture) {
            const text = gesture && typeof gesture.readText === 'function' ? gesture.readText() : '';
            if (!text || text.trim() === '') return;
            selectedTextForSurface = text;
            if (typeof gesture.open === 'function') gesture.open();
          },
        }), 'ai-terminal: selection interaction');
        ctx.slots.inject('conversation.chat.text-interaction', () => ctx.slots.register({
          name: 'conversation.chat.text-interaction',
          key: SELECTION_INTERACTION_ID,
          locale: NS,
        }, AskSelectionAction));
      }

      // --- Bot control room surfaces (plan Task 26) --------------------
      //
      // Kill switch in the session header (visible only while a bot is not
      // stopped), live card at the turn tail, mini card in the input dock and
      // the "Боты" right-panel tab.
      ctx.slots.inject('conversation.session.header.actions', () => ctx.slots.register({
        name: 'conversation.session.header.actions',
        id: 'ai-terminal-bot-kill-switch',
        locale: NS,
      }, BotKillSwitchAction));
      ctx.slots.inject('conversation.chat.turnTail', () => ctx.slots.register({
        name: 'conversation.chat.turnTail',
        id: 'ai-terminal-bots',
        locale: NS,
      }, BotTailCard));
      ctx.slots.inject('conversation.input.dock', () => ctx.slots.register({
        name: 'conversation.input.dock',
        id: 'ai-terminal-bots',
        order: 30,
        locale: NS,
      }, BotDockCard));
      if (ctx.sidebarRightTabs && typeof ctx.sidebarRightTabs.register === 'function') {
        ctx.effect(() => ctx.sidebarRightTabs.register({
          id: 'ai-terminal/bots',
          kind: 'ai-terminal-bots',
          title: () => 'Боты',
        }), 'ai-terminal: bots tab type');
      }
      ctx.slots.inject('sidebar.right.pane.tab', () => ctx.slots.register({
        name: 'sidebar.right.pane.tab',
        key: 'ai-terminal/bots',
        locale: NS,
      }, BotsPanelTabBody));
    }

    return { inject: ['slots', 'locale'], apply };
  },
});
