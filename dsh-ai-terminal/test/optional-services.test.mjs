// Boot contract: the plugin must activate on the shipped Harness profiles even
// when the optional services are absent. The desktop/web profiles do not mount
// a workflowEngine service; before this test the plugin required it, stayed
// pending and DSH web boot failed with "1 entry did not activate".

import assert from "node:assert/strict";
import { test } from "node:test";

import { apply, inject } from "../index.js";

function minimalCtx(options = {}) {
  const routes = [];
  const effects = [];
  const ctx = {
    routes,
    effects,
    webServer: {
      register(route) {
        routes.push(route);
        return () => {
          const index = routes.indexOf(route);
          if (index >= 0) routes.splice(index, 1);
        };
      },
      getOrigin: () => "http://127.0.0.1:3080",
    },
    get(name) {
      if (options.services && Object.hasOwn(options.services, name)) return options.services[name];
      if (typeof ctx[name] === "object" && ctx[name] !== null) return ctx[name];
      return undefined;
    },
    effect(fn) {
      const dispose = fn();
      if (typeof dispose === "function") effects.push(dispose);
      return dispose;
    },
  };
  return ctx;
}

const CONFIG = { mode: "offline", autoStart: false, port: 18190 };

test("the plugin row requires only webServer", () => {
  assert.deepEqual(inject, ["webServer"]);
});

test("apply activates without workflowEngine/schedule/sessionController/agents", () => {
  const ctx = minimalCtx();
  const warnings = [];
  const originalWarn = console.warn;
  console.warn = (...args) => warnings.push(args.join(" "));
  try {
    assert.doesNotThrow(() => apply(ctx, CONFIG));
  } finally {
    console.warn = originalWarn;
  }
  const paths = ctx.routes.map((route) => route.path);
  assert.ok(paths.includes("/ai-terminal-ctl/status"), "status route must be mounted");
  assert.ok(paths.includes("/ai-terminal-ctl/autonomy/status"), "autonomy status route must be mounted");
  assert.ok(paths.includes("/ai-terminal-ctl/autonomy/run"), "autonomy run route must be mounted");
  // The schedule service is absent: ensureSchedule() resolves with a reason, no throw.
  assert.ok(warnings.every((line) => !line.includes("не удалось")), warnings.join("\n"));
});

test("apply activates with the optional services present", () => {
  const registered = [];
  const tools = { register(tool) { registered.push(tool); return () => {}; } };
  const schedule = { list: async () => [], create: async () => ({ id: "task-1" }), edit: async () => ({}) };
  const ctx = minimalCtx({ services: { tools, schedule, workflowEngine: { start: async () => ({ id: "run-1" }) }, sessionController: { prompt() {} }, agents: { get() { return null; } } } });
  assert.doesNotThrow(() => apply(ctx, { ...CONFIG, autonomy: { enabled: false } }));
  assert.ok(registered.some((tool) => tool.name === "terminal_nightly_research"), "nightly tool must be registered");
});
test("tools are mounted through a child ctx.inject", () => {
  const registered = [];
  const tools = { register(tool) { registered.push(tool); return () => {}; } };
  const injected = [];
  const ctx = minimalCtx();
  ctx.inject = (deps, callback) => {
    injected.push(deps);
    const child = minimalCtx({ services: { tools } });
    child.get = (name) => (name === "tools" ? tools : undefined);
    callback(child);
  };
  assert.doesNotThrow(() => apply(ctx, { ...CONFIG, autonomy: { enabled: false } }));
  assert.deepEqual(injected, [["tools"]], "the tool registry must be mounted as an optional child");
  assert.ok(registered.some((tool) => tool.name === "terminal_nightly_research"), "nightly tool must be registered through the child");
});

test("apply survives a deferred tools service", () => {
  let pending = null;
  const ctx = minimalCtx();
  ctx.inject = (deps, callback) => {
    assert.deepEqual(deps, ["tools"]);
    pending = callback;
  };
  assert.doesNotThrow(() => apply(ctx, { ...CONFIG, autonomy: { enabled: false } }));
  assert.equal(typeof pending, "function", "the child callback must be registered");
  // Later, when tools still does not appear, the child callback must stay inert.
  assert.doesNotThrow(() => pending(minimalCtx()));
});
