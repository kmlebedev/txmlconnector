// Executes the nightly research workflow body (task 21) with stub engine hooks:
// the real DSH workflow engine is not available in unit tests, but the script
// contract (phase/agent hooks, 7 phases, JSON summary, no mutating actions in
// the prompts) can be verified here. The file is a function body with top-level
// await and a final `return`, exactly as ctx.workflowEngine.start receives it.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";

const file = path.resolve(import.meta.dirname, "..", "workflows", "nightly-research.js");
const body = fs.readFileSync(file, "utf8");

async function runWorkflow() {
  const phases = [];
  const prompts = [];
  const agent = async (prompt, options = {}) => {
    prompts.push({ prompt, label: options.label });
    return "stub:" + (options.label || "agent");
  };
  const phase = (title, detail) => phases.push({ title, detail });
  const parallel = async (items) => Promise.all(items);
  const pipeline = async (items, fn) => Promise.all(items.map(fn));
  const log = () => {};
  const fn = new Function("agent", "parallel", "pipeline", "phase", "log", "return (async () => {\n" + body + "\n})();");
  const result = await fn(agent, parallel, pipeline, phase, log);
  return { result, phases, prompts };
}

test("nightly workflow body runs all phases and returns the summary", async () => {
  const { result, phases, prompts } = await runWorkflow();
  assert.equal(phases.length, 7, "workflow must announce all 7 phases");
  assert.equal(prompts.length, 6, "workflow must run one agent per research phase");
  assert.deepEqual(
    phases.map((p) => p.title),
    ["Данные", "Волатильность", "Тренд", "Роботы", "Паттерны", "Автоматизация", "Итог"],
  );
  assert.equal(result.workflow, "ai-terminal-nightly");
  assert.ok(result.summary.includes("Ночное исследование"));
  for (const key of ["data", "volatility", "trend", "algos", "patterns", "automation"]) {
    assert.equal(result[key], "stub:" + key.replace("data", "data-audit").replace("volatility", "volatility-recalibration").replace("trend", "walk-forward").replace("algos", "execution-algos").replace("patterns", "skill-patterns").replace("automation", "automation-proposals"));
  }
});

test("nightly workflow prompts call the terminal tools and stay read-only", async () => {
  const { prompts } = await runWorkflow();
  const all = prompts.map((p) => p.prompt).join("\n");
  for (const tool of [
    "terminal_list_instruments",
    "terminal_get_history",
    "terminal_vol_forecast",
    "terminal_walk_forward",
    "terminal_detect_execution_algos",
    "terminal_list_skill_patterns",
    "terminal_propose_automation",
  ]) {
    assert.ok(all.includes(tool), "workflow must use " + tool);
  }
  assert.ok(!/terminal_place_order|terminal_cancel_order|activate live/i.test(all), "workflow must not trade");
});