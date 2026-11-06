// autonomy.js — autonomous nightly research workflow of the AI terminal
// (plan Task 21).
//
// Host half responsibilities:
// - owns the "ai-terminal-nightly" workflow run: it passes the script text from
//   workflows/nightly-research.js to ctx.workflowEngine and tracks its result;
// - enforces the token budget by intercepting the ctx.llm "llm/stream"
//   waterfall and summing the usage chunks while the run is active, cancelling
//   the run with reason "token-budget" when the budget is exceeded; the wall
//   clock timer cancels with "max-wall";
// - is the kill switch: cancel("kill-switch") plus the backend stop_autonomy
//   tool through the plugin tool bridge;
// - creates the Harness schedule (ctx.schedule) "AI-терминал: ночное
//   исследование" at 00:30 Europe/Moscow on nights after trading sessions
//   (cron "30 0 * * 2-6", Tue-Sat), idempotently by title; disabled autonomy
//   pauses an existing task instead of creating one;
// - registers the terminal_nightly_research Harness tool, so the scheduled
//   session agent starts the workflow deterministically (the schedule prompt
//   asks for exactly that tool).
//
// Facts are verified in docs/ai-terminal/dsh-integration.md §2.4-2.6:
// ctx.workflowEngine.start({ script, meta, args?, parent, signal? }), the run
// exposes id/result/cancel/dispose, ctx.schedule.create/list/edit and the
// llm/stream waterfall. The plan's timing.expr and Asia/Moscow are corrected
// here (cron selector + Europe/Moscow) and documented in README.

/** Human title of the scheduled nightly task. */
export const NIGHTLY_TASK_TITLE = 'AI-терминал: ночное исследование';

/** Harness tool the scheduled session agent calls to start the workflow. */
export const NIGHTLY_TOOL_NAME = 'terminal_nightly_research';

/** Cron timing of the nightly run: 00:30 Europe/Moscow, Tue-Sat (after Mon-Fri evenings). */
export const NIGHTLY_CRON = Object.freeze({
  kind: 'cron',
  cron: Object.freeze({ expression: '30 0 * * 2-6', time_zone: 'Europe/Moscow' }),
});

/** Workflow identity block (validated by the engine before the run exists). */
export const NIGHTLY_META = Object.freeze({
  name: 'ai-terminal-nightly',
  description: 'Ночное исследование AI-терминала: данные, волатильность, walk-forward, роботы, паттерны, автоматизация.',
  whenToUse: 'Ночной автономный цикл после вечерней торговой сессии (расписание 00:30 Europe/Moscow).',
  phases: Object.freeze([
    Object.freeze({ title: 'Данные', detail: 'проверка записи и разрывов за день' }),
    Object.freeze({ title: 'Волатильность', detail: 'перекалибровка оракула на asOf=now' }),
    Object.freeze({ title: 'Тренд', detail: 'walk-forward по универсуму' }),
    Object.freeze({ title: 'Роботы', detail: 'отчёт detect_execution_algos' }),
    Object.freeze({ title: 'Паттерны', detail: 'черновики навыков и Promote' }),
    Object.freeze({ title: 'Автоматизация', detail: 'предложения повторяющихся задач' }),
    Object.freeze({ title: 'Итог', detail: 'сводка в сессию' }),
  ]),
});

/** Prompt delivered by the Harness schedule into the nightly session. */
export const NIGHTLY_PROMPT = 'Запусти автономное ночное исследование AI-терминала: вызови инструмент '
  + NIGHTLY_TOOL_NAME + ' без аргументов и коротко перескажи полученную сводку. '
  + 'Мутирующие действия не исполняй: предложения из сводки ждут подтверждения человека.';

/** Readable one-line summary of a workflow result (used by the tool renderer). */
export function summaryText(value) {
  if (!value || typeof value !== 'object') return 'Ночное исследование: пустой результат.';
  const result = value.result && typeof value.result === 'object' ? value.result : null;
  if (result && typeof result.summary === 'string' && result.summary !== '') return result.summary;
  const usage = value.usage && typeof value.usage === 'object' ? value.usage : null;
  const parts = ['Ночное исследование AI-терминала:'];
  if (value.stopReason === 'cancelled') parts.push('отменено (' + String(value.reason || 'stop') + ').');
  else if (value.stopReason === 'error') parts.push('завершилось ошибкой.');
  else parts.push('выполнено.');
  if (usage && Number.isFinite(usage.total)) parts.push('токенов ' + String(usage.total) + '.');
  if (value.budget && Number.isFinite(value.budget.tokenBudget)) parts.push('бюджет ' + String(value.budget.tokenBudget) + '.');
  const preview = result === null ? '' : JSON.stringify(result).slice(0, 1200);
  if (preview !== '') parts.push('Результат: ' + preview);
  return parts.join(' ');
}

/** Normalize the schedule list reply to an items array. */
function scheduleItems(listed) {
  if (Array.isArray(listed)) return listed;
  if (listed && Array.isArray(listed.items)) return listed.items;
  return [];
}

/**
 * Create the autonomy controller of one plugin instance.
 *
 * options: { ctx, workflowEngine, schedule, invokeTool?, cfg?, scriptText?,
 *            log?, now? }
 * The returned object is inert without a workflow engine or a script text: the
 * tool then reports the reason instead of starting a run.
 */
export function createAutonomy(options = {}) {
  const ctx = options.ctx || {};
  const engine = options.workflowEngine;
  const schedule = options.schedule;
  const invokeTool = typeof options.invokeTool === 'function' ? options.invokeTool : null;
  const scriptText = typeof options.scriptText === 'string' ? options.scriptText : '';
  const log = typeof options.log === 'function' ? options.log : () => {};
  const now = typeof options.now === 'function' ? options.now : () => Date.now();
  const cfg = {
    enabled: false,
    tokenBudget: 200_000,
    maxWallMinutes: 60,
    approvalMode: 'confirm',
    ...(options.cfg || {}),
  };

  const state = {
    running: false,
    runId: null,
    startedAt: null,
    lastFinishedAt: null,
    stopReason: null,
    reason: null,
    error: null,
    lastResult: null,
    scheduleId: null,
    stopAutonomy: null,
    budgetTripped: false,
    usage: { input: 0, output: 0, total: 0 },
  };
  let runRef = null;
  let completion = null;
  let wallTimer = null;

  function status() {
    return {
      enabled: cfg.enabled === true,
      running: state.running,
      runId: state.runId,
      startedAt: state.startedAt,
      lastFinishedAt: state.lastFinishedAt,
      stopReason: state.stopReason,
      reason: state.reason,
      error: state.error,
      usage: { ...state.usage },
      budget: { tokenBudget: cfg.tokenBudget, maxWallMinutes: cfg.maxWallMinutes },
      scheduleId: state.scheduleId,
      approvalMode: cfg.approvalMode,
      toolName: NIGHTLY_TOOL_NAME,
      lastResult: state.lastResult,
      stopAutonomy: state.stopAutonomy,
    };
  }

  /** Add one usage sample and trip the budget once when it is exceeded. */
  function addUsage(usage) {
    if (!usage || typeof usage !== 'object') return;
    const input = Number.isFinite(usage.inputTokens) ? usage.inputTokens : 0;
    const output = Number.isFinite(usage.outputTokens) ? usage.outputTokens : 0;
    const total = Number.isFinite(usage.totalTokens) ? usage.totalTokens : input + output;
    state.usage.input += input;
    state.usage.output += output;
    state.usage.total += total;
    if (cfg.tokenBudget > 0 && state.usage.total > cfg.tokenBudget && !state.budgetTripped) {
      state.budgetTripped = true;
      log('warn', 'ai-terminal: ночью превышен бюджет токенов (' + state.usage.total + ' > ' + cfg.tokenBudget + '), workflow отменяется');
      cancelRun('token-budget');
    }
  }

  /** Wrap one raw LLM chunk stream with usage accounting. */
  function meterStream(stream) {
    return (async function* metered() {
      for await (const chunk of stream) {
        if (chunk && chunk.type === 'usage') addUsage(chunk.usage);
        yield chunk;
      }
    })();
  }

  /** Intercept ctx.llm streams only while a nightly run is active. */
  function installMeter() {
    if (typeof ctx.on !== 'function') return () => {};
    return ctx.on('llm/stream', (_options, next) => {
      if (!state.running) return next();
      return meterStream(next());
    });
  }

  function stopWallClock() {
    if (wallTimer !== null) {
      clearTimeout(wallTimer);
      wallTimer = null;
    }
  }

  /** Cancel the active run with one stable reason (budget, wall clock, kill switch). */
  function cancelRun(reason) {
    if (!state.running) return false;
    state.stopReason = 'cancelled';
    state.reason = reason;
    try {
      if (runRef && typeof runRef.cancel === 'function') runRef.cancel(reason);
    } catch (error) {
      log('warn', 'ai-terminal: не удалось отменить workflow: ' + (error instanceof Error ? error.message : String(error)));
    }
    return true;
  }

  function startWallClock() {
    if (!(cfg.maxWallMinutes > 0)) return;
    wallTimer = setTimeout(() => {
      if (!state.running) return;
      log('warn', 'ai-terminal: ночной workflow превысил maxWallMinutes=' + cfg.maxWallMinutes + ', отменяется');
      cancelRun('max-wall');
    }, cfg.maxWallMinutes * 60_000);
    if (wallTimer && typeof wallTimer.unref === 'function') wallTimer.unref();
  }

  /**
   * Start the nightly workflow once and resolve with the run status. A run
   * that is already active is not started twice.
   */
  async function runNow(runOptions = {}) {
    if (state.running) return status();
    if (!engine || typeof engine.start !== 'function') throw new Error('workflowEngine Harness недоступен');
    if (scriptText.trim() === '') throw new Error('скрипт ночного workflow не найден (workflows/nightly-research.js)');
    state.running = true;
    state.runId = null;
    state.startedAt = now();
    state.lastFinishedAt = null;
    state.stopReason = null;
    state.reason = null;
    state.error = null;
    state.lastResult = null;
    state.stopAutonomy = null;
    state.budgetTripped = false;
    state.usage = { input: 0, output: 0, total: 0 };
    const aborter = new AbortController();
    if (runOptions.signal) {
      if (runOptions.signal.aborted) aborter.abort();
      else runOptions.signal.addEventListener('abort', () => aborter.abort(), { once: true });
    }
    let resolveCompletion = null;
    completion = new Promise((resolve) => { resolveCompletion = resolve; });
    startWallClock();
    let activeRun = null;
    try {
      activeRun = await engine.start({
        script: scriptText,
        meta: NIGHTLY_META,
        args: runOptions.args || {},
        parent: runOptions.parent,
        signal: aborter.signal,
      });
      runRef = activeRun;
      state.runId = activeRun && activeRun.id ? activeRun.id : null;
      const result = await activeRun.result;
      state.lastResult = result === undefined ? null : result;
      if (result && typeof result === 'object') {
        if (typeof result.stopReason === 'string') state.stopReason = result.stopReason;
        if (typeof result.reason === 'string' && state.reason === null) state.reason = result.reason;
      }
    } catch (error) {
      if (state.stopReason === null) {
        state.stopReason = 'error';
        state.reason = 'engine-error';
      }
      state.error = error instanceof Error ? error.message : String(error);
      log('warn', 'ai-terminal: ночной workflow завершился ошибкой: ' + state.error);
    } finally {
      stopWallClock();
      try {
        if (activeRun && typeof activeRun.dispose === 'function') await activeRun.dispose();
      } catch (error) {
        log('warn', 'ai-terminal: dispose workflow: ' + (error instanceof Error ? error.message : String(error)));
      }
      state.running = false;
      state.lastFinishedAt = now();
      runRef = null;
      if (typeof resolveCompletion === 'function') resolveCompletion();
      completion = null;
    }
    return status();
  }

  /**
   * Kill switch: cancel the active run and stop the backend autonomy runner.
   * Returns the status snapshot with the stop_autonomy outcome.
   */
  async function stop() {
    if (state.running) {
      cancelRun('kill-switch');
      if (completion && typeof completion.then === 'function') {
        try {
          await completion;
        } catch {
          // the run settles through its own path
        }
      }
    }
    let stopAutonomy = null;
    if (invokeTool) {
      try {
        await invokeTool('stop_autonomy', {});
        stopAutonomy = true;
      } catch (error) {
        stopAutonomy = false;
        log('warn', 'ai-terminal: stop_autonomy не выполнен: ' + (error instanceof Error ? error.message : String(error)));
      }
    }
    state.stopAutonomy = stopAutonomy;
    return status();
  }

  /**
   * Create or reuse the nightly Harness schedule. With autonomy disabled no
   * task is created and an existing one is paused (plan Task 21).
   */
  async function ensureSchedule() {
    if (!schedule || typeof schedule.list !== 'function' || typeof schedule.create !== 'function') {
      return { created: false, taskId: null, reason: 'schedule Harness недоступен' };
    }
    let items = [];
    try {
      items = scheduleItems(await schedule.list({ active: true }));
    } catch (error) {
      log('warn', 'ai-terminal: список расписаний недоступен: ' + (error instanceof Error ? error.message : String(error)));
      return { created: false, taskId: null, reason: 'schedule list failed' };
    }
    const existing = items.find((item) => item && item.title === NIGHTLY_TASK_TITLE) || null;
    if (cfg.enabled !== true) {
      if (existing && typeof schedule.edit === 'function') {
        try {
          await schedule.edit({ id: existing.id, action: 'update', patch: { active: false } });
        } catch (error) {
          log('warn', 'ai-terminal: не удалось поставить расписание на паузу: ' + (error instanceof Error ? error.message : String(error)));
        }
      }
      state.scheduleId = existing === null ? null : existing.id;
      return { created: false, taskId: state.scheduleId, disabled: true };
    }
    if (existing !== null) {
      state.scheduleId = existing.id;
      return { created: false, taskId: existing.id };
    }
    const task = await schedule.create({
      title: NIGHTLY_TASK_TITLE,
      prompt: NIGHTLY_PROMPT,
      timing: NIGHTLY_CRON,
      target: { kind: 'new_once' },
    });
    state.scheduleId = task && task.id ? task.id : null;
    return { created: true, taskId: state.scheduleId };
  }

  /**
   * Register the terminal_nightly_research Harness tool. The scheduled session
   * agent calls it; the call blocks until the workflow settles and returns the
   * summary so the agent can report it in the session.
   */
  function registerTool(toolsService) {
    if (!toolsService || typeof toolsService.register !== 'function') return () => {};
    return toolsService.register({
      name: NIGHTLY_TOOL_NAME,
      description: 'Запустить автономное ночное исследование AI-терминала: данные, волатильность, '
        + 'walk-forward, роботы, паттерны, автоматизация и сводка. Мутирующие действия не выполняются — '
        + 'предложения ждут подтверждения человека.',
      parameters: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      output: {
        schema: {},
        render: (_args, value) => [{ type: 'text', text: summaryText(value) }],
      },
      async execute(_args, exec) {
        const snapshot = await runNow({ trigger: 'tool', signal: exec && exec.signal });
        return {
          result: snapshot.lastResult,
          stopReason: snapshot.stopReason,
          reason: snapshot.reason,
          usage: snapshot.usage,
          budget: snapshot.budget,
        };
      },
    });
  }

  const meterDispose = installMeter();

  function dispose() {
    stopWallClock();
    if (state.running) cancelRun('dispose');
    if (typeof meterDispose === 'function') {
      try {
        meterDispose();
      } catch {
        // the owning context already released the listener
      }
    }
  }

  return { runNow, stop, status, ensureSchedule, registerTool, dispose };
}
