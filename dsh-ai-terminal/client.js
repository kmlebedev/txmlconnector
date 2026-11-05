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

    function TerminalPanel({ t }) {
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
    }

    return { inject: ['slots', 'locale'], apply };
  },
});
