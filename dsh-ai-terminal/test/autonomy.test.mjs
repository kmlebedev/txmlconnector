// Tests of the autonomous nightly research workflow (plan Task 21): the token
// budget stop, the schedule gate, the kill switch and the nightly tool.
// Fake workflow engine / schedule / LLM waterfall — no live Harness.

import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  createAutonomy,
  NIGHTLY_CRON,
  NIGHTLY_PROMPT,
  NIGHTLY_TASK_TITLE,
  NIGHTLY_TOOL_NAME,
} from '../autonomy.js';

/** One macrotask turn, enough for a started run to be awaiting its result. */
function tick() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

/** Fake workflow run whose result resolves when cancelled. */
function fakeRun(id = 'run-1') {
  let resolveResult;
  const run = {
    id,
    cancelReason: null,
    disposed: 0,
    result: new Promise((resolve) => { resolveResult = resolve; }),
    cancel(reason) {
      run.cancelReason = reason;
      resolveResult({ stopReason: 'cancelled', reason });
    },
    dispose: async () => { run.disposed += 1; },
  };
  return run;
}

/** Fake ctx exposing only the llm/stream waterfall registration. */
function fakeCtx() {
  const captured = { listener: null, disposers: 0 };
  return {
    captured,
    on(name, listener) {
      assert.equal(name, 'llm/stream');
      captured.listener = listener;
      return () => { captured.disposers += 1; };
    },
  };
}

/** Feed one usage chunk through the captured llm/stream listener. */
async function feedUsage(ctx, tokens) {
  assert.ok(ctx.captured.listener, 'the autonomy must install the llm/stream meter');
  const stream = ctx.captured.listener({}, () => (async function* generate() {
    yield { type: 'usage', usage: { inputTokens: tokens, outputTokens: 0, totalTokens: tokens } };
  })());
  for await (const _chunk of stream) {
    // drain
  }
}

/** Build one autonomy instance with fake collaborators. */
function makeAutonomy(overrides = {}) {
  const ctx = overrides.ctx || fakeCtx();
  return createAutonomy({
    ctx,
    cfg: { enabled: false, tokenBudget: 200000, maxWallMinutes: 60, approvalMode: 'confirm', ...overrides.cfg },
    workflowEngine: overrides.workflowEngine || { start: () => fakeRun() },
    schedule: overrides.schedule,
    scriptText: 'return { ok: true }',
    invokeTool: overrides.invokeTool,
    log: () => {},
    now: overrides.now,
  });
}

test('nightly workflow stops on token budget', async () => {
  const ctx = fakeCtx();
  const run = fakeRun();
  const engine = {
    start(options) {
      assert.equal(options.script, 'return { ok: true }');
      assert.equal(options.meta.name, 'ai-terminal-nightly');
      return run;
    },
  };
  const autonomy = makeAutonomy({ ctx, workflowEngine: engine });
  const pending = autonomy.runNow({ trigger: 'manual' });
  await tick();
  assert.equal(run.cancelReason, null);

  await feedUsage(ctx, 120000);
  assert.equal(run.cancelReason, null, 'under the budget the run must continue');

  await feedUsage(ctx, 120000);
  assert.equal(run.cancelReason, 'token-budget');

  const status = await pending;
  assert.equal(status.running, false);
  assert.equal(status.stopReason, 'cancelled');
  assert.equal(status.reason, 'token-budget');
  assert.equal(status.usage.total, 240000);
  assert.equal(status.budget.tokenBudget, 200000);
  assert.equal(run.disposed, 1);
});

test('schedule is not created when autonomy.enabled is false', async () => {
  let created = 0;
  const edited = [];
  const schedule = {
    async list() { return { items: [] }; },
    async create() { created += 1; return { id: 'task-1' }; },
    async edit(request) { edited.push(request); return {}; },
  };
  const autonomy = makeAutonomy({ schedule });
  const result = await autonomy.ensureSchedule();
  assert.equal(created, 0);
  assert.equal(result.created, false);
  assert.equal(edited.length, 0);

  // A previously created task is paused, not recreated.
  const activeTask = { id: 'task-old', title: NIGHTLY_TASK_TITLE, active: true };
  const disableAutonomy = makeAutonomy({
    schedule: {
      async list() { return { items: [activeTask] }; },
      async create() { created += 1; return { id: 'unexpected' }; },
      async edit(request) { edited.push(request); return {}; },
    },
  });
  const paused = await disableAutonomy.ensureSchedule();
  assert.equal(created, 0);
  assert.equal(paused.created, false);
  assert.equal(edited.length, 1);
  assert.equal(edited[0].id, 'task-old');
  assert.equal(edited[0].action, 'update');
  assert.equal(edited[0].patch.active, false);
});

test('kill switch cancels the running workflow and stop_autonomy', async () => {
  const ctx = fakeCtx();
  const run = fakeRun();
  const invoked = [];
  const autonomy = makeAutonomy({
    ctx,
    workflowEngine: { start: () => run },
    invokeTool: async (name, args) => {
      invoked.push({ name, args });
      return { result: { stopped: true } };
    },
  });
  const pending = autonomy.runNow({ trigger: 'manual' });
  await tick();
  const status = await autonomy.stop();
  assert.equal(run.cancelReason, 'kill-switch');
  assert.equal(invoked.length, 1);
  assert.equal(invoked[0].name, 'stop_autonomy');
  assert.equal(status.running, false);
  assert.equal(status.reason, 'kill-switch');
  assert.equal(status.stopAutonomy, true);
  await pending;
});

test('nightly schedule is created once with the plan cron and new_once target', async () => {
  const createdTasks = [];
  const schedule = {
    async list() { return { items: createdTasks }; },
    async create(request) {
      const task = { id: 'task-' + (createdTasks.length + 1), ...request, active: true };
      createdTasks.push(task);
      return task;
    },
    async edit() { return {}; },
  };
  const autonomy = makeAutonomy({ cfg: { enabled: true }, schedule });
  const first = await autonomy.ensureSchedule();
  assert.equal(first.created, true);
  assert.equal(createdTasks.length, 1);
  const task = createdTasks[0];
  assert.equal(task.title, NIGHTLY_TASK_TITLE);
  assert.equal(task.prompt, NIGHTLY_PROMPT);
  assert.deepEqual(task.timing, NIGHTLY_CRON);
  assert.deepEqual(task.timing, { kind: 'cron', cron: { expression: '30 0 * * 2-6', time_zone: 'Europe/Moscow' } });
  assert.deepEqual(task.target, { kind: 'new_once' });

  const second = await autonomy.ensureSchedule();
  assert.equal(second.created, false);
  assert.equal(second.taskId, 'task-1');
  assert.equal(createdTasks.length, 1, 'the schedule must be idempotent by title');
});

test('nightly tool is registered and runs the workflow', async () => {
  const ctx = fakeCtx();
  const run = fakeRun();
  const registered = [];
  const autonomy = makeAutonomy({
    ctx,
    cfg: { enabled: true },
    workflowEngine: { start: () => run },
  });
  const dispose = autonomy.registerTool({
    register(definition) { registered.push(definition); return () => {}; },
  });
  assert.equal(registered.length, 1);
  const definition = registered[0];
  assert.equal(definition.name, NIGHTLY_TOOL_NAME);
  assert.equal(definition.parameters.type, 'object');
  const result = definition.execute({}, {});
  assert.ok(result && typeof result.then === 'function', 'the tool execute must be async');
  await tick();
  assert.equal(run.cancelReason, null);
  run.cancel('test-finish');
  const value = await result;
  assert.equal(value.stopReason, 'cancelled');
  assert.equal(value.reason, 'test-finish');
  assert.deepEqual(value.usage, { input: 0, output: 0, total: 0 });
  dispose();
});
