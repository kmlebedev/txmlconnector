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
      const meta = block && block.meta && block.meta.kind === 'backtest' ? block.meta : null;
      if (phase !== 'result' || meta === null) return toolCardWaiting('Бэктест выполняется…');
      const metrics = meta.metrics || {};
      const points = equityPolyline(Array.isArray(meta.equity) ? meta.equity : []);
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
          : null);
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
    }

    return { inject: ['slots', 'locale'], apply };
  },
});
