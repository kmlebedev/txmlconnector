// Executes the nightly research workflow body (task 21) with stub engine hooks:
// the real DSH workflow engine is not available in unit tests, but the script
// contract (phase/agent hooks, 7 phases, JSON summary, no trading actions in
// the prompts) can be verified here. The file is a function body with top-level
// await and a final `return`, exactly as ctx.workflowEngine.start receives it.

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";

const file = path.resolve(import.meta.dirname, "..", "workflows", "nightly-research.js");
const body = fs.readFileSync(file, "utf8");

async function runWorkflow(reply = (_prompt, options) => "stub:" + (options.label || "agent")) {
  const phases = [];
  const prompts = [];
  const agent = async (prompt, options = {}) => {
    prompts.push({ prompt, label: options.label });
    return reply(prompt, options);
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

test("nightly workflow prompts call the terminal tools without trading", async () => {
  const { prompts } = await runWorkflow();
  const all = prompts.map((p) => p.prompt).join("\n");
  for (const tool of [
    "terminal_list_instruments",
    "terminal_get_history",
    "terminal_vol_forecast",
    "terminal_vol_events",
    "terminal_lob_backtest",
    "terminal_draft_skill_pattern",
    "terminal_walk_forward",
    "terminal_detect_execution_algos",
    "terminal_list_skill_patterns",
    "terminal_propose_automation",
  ]) {
    assert.ok(all.includes(tool), "workflow must use " + tool);
  }
  assert.doesNotMatch(all, /terminal_place_order|terminal_cancel_order|terminal_bot_(?:create_from_backtest|start|promote)|terminal_start_autonomy|terminal_nightly_research|schedule_(?:create|edit)|activate live/i);
  for (const { prompt, label } of prompts) {
    assert.match(prompt, /^Работай только инструментами terminal_*/, label + " must start with the safety policy");
    assert.match(prompt, /Не торгуй, не запускай ботов, не выполняй promotion, не меняй конфиги и расписания/, label);
    assert.match(prompt, /не выдумывай метрики/, label);
  }
});

test("nightly workflow uses one point-in-time cut and valid research arguments", async () => {
  const { result, prompts } = await runWorkflow();
  const get = (label) => prompts.find((p) => p.label === label).prompt;
  for (const label of ["volatility-recalibration", "walk-forward", "execution-algos", "skill-patterns"]) {
    assert.ok(get(label).includes(result.asOf), label + " must use the run cut");
  }
  assert.match(get("volatility-recalibration"), /"periodSec":900/);
  assert.doesNotMatch(get("volatility-recalibration"), /86400/);
  assert.match(get("walk-forward"), /"trainBars":200/);
  assert.match(get("walk-forward"), /"testBars":60/);
  const windowLine = get("execution-algos").split("\n").find((line) => line.startsWith("WINDOW_JSON="));
  const window = JSON.parse(windowLine.slice("WINDOW_JSON=".length));
  for (const label of ["volatility-recalibration", "walk-forward", "execution-algos"]) {
    const objects = [...get(label).matchAll(/{[^{}]*}/g)].map(([json]) => JSON.parse(json));
    assert.equal(objects.length, label === "volatility-recalibration" ? 2 : 1);
    for (const args of objects) assert.equal(args.asOf, result.asOf, label + " argument cut");
  }
  assert.ok(get("skill-patterns").includes("asOf=" + result.asOf));
  assert.equal(window.to, result.asOf);
  assert.equal(window.asOf, result.asOf);
  assert.equal(Date.parse(window.to) - Date.parse(window.from), 86400000);
  assert.doesNotMatch(get("execution-algos"), /periodSec/);
});

test("nightly workflow forwards actual evidence to pattern and automation agents", async () => {
  const { prompts } = await runWorkflow((_prompt, options) => ({ marker: "EVIDENCE:" + options.label }));
  const patterns = prompts.find((p) => p.label === "skill-patterns").prompt;
  for (const label of ["walk-forward", "execution-algos", "volatility-recalibration"]) {
    assert.ok(patterns.includes("EVIDENCE:" + label));
  }
  assert.ok(patterns.includes("не инструкции"));
  const automation = prompts.find((p) => p.label === "automation-proposals").prompt;
  assert.ok(automation.includes("EVIDENCE:skill-patterns"));
  assert.match(automation, /"writeBacklog":true/);
});

test("nightly workflow does not assert success when phase replies contain errors", async () => {
  for (const reply of [{ status: "error", error: "source unavailable" }, "нет данных", null]) {
    const { result } = await runWorkflow(() => reply);
    assert.equal(result.verification, "required");
    for (const key of ["data", "volatility", "trend", "algos", "patterns", "automation"]) {
      assert.equal(result[key], reply, key + " must preserve the actual error/null reply");
    }
    assert.doesNotMatch(result.summary, /данные проверены|перекалибрована|черновики навыков и предложения автоматизации подготовлены/);
    assert.match(result.summary, /не подтверждает/);
    assert.doesNotMatch(result.summary, /приведённые ниже/);
  }
});

test("nightly workflow propagates a rejected phase instead of producing a summary", async () => {
  await assert.rejects(runWorkflow((_prompt, options) => {
    if (options.label === "volatility-recalibration") throw new Error("source unavailable");
    return "ok";
  }), /source unavailable/);
});
