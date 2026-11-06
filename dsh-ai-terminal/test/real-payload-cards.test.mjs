// Regression tests for the chat cards using payloads captured from the real
// offline backend (terminal -mode offline, deterministic seed): the tool
// bridge reply shapes, the DOM-free card models and the bot journal models are
// asserted without a Harness or browser. These payloads are the contract the
// client.js toolview components consume (plan Tasks 9, 24, 26).

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { pathToFileURL } from "node:url";

import { renderToolResult, toolPresentationMeta } from "../tools.js";
import {
  botCardData,
  botRenderText,
  isSignificantEvent,
  journalLine,
  parseBotFromReply,
} from "../bot-ui.js";

const fixtures = JSON.parse(
  fs.readFileSync(path.join(import.meta.dirname, "fixtures", "real-cards.json"), "utf8"),
);

test("real quote payload renders the quote card model", () => {
  const args = { symbol: "TQBR:SBER" };
  const meta = toolPresentationMeta(args, fixtures.quote, "terminal_get_quote");
  assert.equal(meta.kind, "quote");
  assert.equal(meta.symbol, "TQBR:SBER");
  assert.equal(typeof meta.last, "number");
  assert.ok(meta.last > 0);
  const parts = renderToolResult(args, fixtures.quote, "terminal_get_quote");
  assert.ok(parts[0].text.includes("TQBR:SBER"), parts[0].text);
});

test("real backtest payload renders the backtest card model and text", () => {
  const args = { symbol: "TQBR:SBER", periodSec: 300 };
  const meta = toolPresentationMeta(args, fixtures.backtest, "terminal_backtest_rules");
  assert.equal(meta.kind, "backtest");
  assert.equal(meta.symbol, "TQBR:SBER");
  assert.equal(meta.periodSec, 300);
  assert.equal(typeof meta.metrics.totalReturnPct, "number");
  assert.equal(typeof meta.metrics.maxDrawdownPct, "number");
  assert.ok(Array.isArray(meta.equity) && meta.equity.length > 0);
  const point = meta.equity[0];
  assert.equal(typeof point.t, "string");
  assert.equal(typeof point.value, "number");
  assert.equal(typeof point.benchmark, "number");
  assert.equal(typeof point.drawdownPct, "number");
  assert.ok(Array.isArray(meta.warnings));
  const parts = renderToolResult(args, fixtures.backtest, "terminal_backtest_rules");
  assert.ok(parts[0].text.includes("Бэктест"), parts[0].text);
  assert.ok(parts[0].text.includes("не инвестиционная рекомендация"), parts[0].text);
});

test("real bot create reply and journal drive the bot card models", () => {
  const bot = parseBotFromReply(fixtures.botCreate);
  assert.equal(bot.id, "bot_0001");
  assert.equal(bot.symbol, "TQBR:SBER");
  assert.equal(bot.periodSec, 300);
  assert.equal(bot.stage, "check");
  const card = botCardData(bot);
  assert.equal(card.id, "bot_0001");
  assert.equal(card.symbol, "TQBR:SBER");

  const entries = fixtures.journal.entries;
  assert.ok(entries.length >= 2, "fixture must keep the stage and closed_bar entries");
  assert.equal(entries[0].kind, "stage");
  assert.equal(isSignificantEvent(entries[0]), true);
  assert.equal(entries[1].kind, "closed_bar");
  assert.equal(isSignificantEvent(entries[1]), false);
  assert.ok(journalLine(entries[1]).includes("close="), journalLine(entries[1]));
  const text = botRenderText("terminal_bot_check", {}, { entries });
  assert.ok(typeof text === "string" && text.length > 0);
});

// --- live Harness Client bundle with the real card meta --------------------

// client.js is a Harness Client bundle: it registers through
// window.__ModuleLoader__.load and requires react. The fake loader/React stub
// let the actual toolview components render the meta produced above from the
// captured backend payloads (no browser needed).

function fakeReact() {
  const createElement = (type, props, ...children) => ({ type, props: props || {}, children: children.flat(Infinity) });
  return {
    createElement,
    memo: (component) => component,
    useState: (initial) => [typeof initial === "function" ? initial() : initial, () => {}],
    useMemo: (factory) => factory(),
    useCallback: (callback) => callback,
    useEffect: () => {},
    useRef: () => ({ current: null }),
    Fragment: "Fragment",
  };
}

let cachedViews = null;

/** Load client.js once and return the map of tool.call.toolview components. */
async function loadToolViews() {
  if (cachedViews) return cachedViews;
  const previousWindow = globalThis.window;
  try {
    let registration;
    globalThis.window = { __ModuleLoader__: { load(spec) { registration = spec; } } };
    const file = path.join(import.meta.dirname, "..", "client.js");
    await import(pathToFileURL(file).href + "?cards=" + Date.now());
    assert.ok(registration, "client.js must register through window.__ModuleLoader__");
    const client = registration.factory((name) => {
      if (name === "react") return fakeReact();
      throw new Error("unexpected require: " + name);
    });
    const views = new Map();
    const ctx = {
      effect(fn) { const dispose = fn(); return typeof dispose === "function" ? dispose : () => {}; },
      locale: { register() {}, bind: () => (key) => key },
      slots: {
        inject(name, factory) { factory(); return () => {}; },
        register(spec, Component) {
          if (spec.name === "tool.call.toolview") views.set(spec.key, Component);
          return () => {};
        },
      },
    };
    client.apply(ctx);
    cachedViews = views;
    return views;
  } finally {
    globalThis.window = previousWindow;
  }
}

function textOf(node) {
  const parts = [];
  const visit = (value) => {
    if (value === null || value === undefined || typeof value === "boolean") return;
    if (typeof value === "string" || typeof value === "number") { parts.push(String(value)); return; }
    if (Array.isArray(value)) { value.forEach(visit); return; }
    if (typeof value === "object" && value.children) visit(value.children);
  };
  visit(node);
  return parts.join(" ");
}

function typesOf(node) {
  const types = [];
  const visit = (value) => {
    if (Array.isArray(value)) { value.forEach(visit); return; }
    if (value && typeof value === "object") { types.push(value.type); if (value.children) visit(value.children); }
  };
  visit(node);
  return types;
}

test("the real backtest payload renders through the Harness Client card", async () => {
  const meta = toolPresentationMeta({ symbol: "TQBR:SBER", periodSec: 300 }, fixtures.backtest, "terminal_backtest_rules");
  const views = await loadToolViews();
  const view = views.get("terminal_run_backtest");
  assert.equal(typeof view, "function", "missing terminal_run_backtest toolview");
  const element = view({
    phase: "result",
    toolName: "terminal_run_backtest",
    callId: "call_1",
    block: { meta, content: [{ type: "text", text: "ok" }], isError: false, subCalls: [] },
  });
  const text = textOf(element);
  assert.match(text, /Бэктест/);
  assert.match(text, /SBER/);
  assert.ok(typesOf(element).includes("polyline"), "the card must draw the equity curve");
});

test("the real quote payload renders through the Harness Client card", async () => {
  const meta = toolPresentationMeta({ symbol: "TQBR:SBER" }, fixtures.quote, "terminal_get_quote");
  const views = await loadToolViews();
  const view = views.get("terminal_get_quote");
  assert.equal(typeof view, "function", "missing terminal_get_quote toolview");
  const element = view({
    phase: "result",
    toolName: "terminal_get_quote",
    callId: "call_2",
    block: { meta, content: [], isError: false, subCalls: [] },
  });
  const text = textOf(element);
  assert.match(text, /TQBR:SBER/);
  const last = fixtures.quote.result.last.toFixed(2).replace(".", ",");
  assert.ok(text.includes(last), "the card must render the fixture quote " + last + ", got: " + text);
});
