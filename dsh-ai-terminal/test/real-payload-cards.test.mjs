// Regression tests for the chat cards using payloads captured from the real
// offline backend (terminal -mode offline, deterministic seed): the tool
// bridge reply shapes, the DOM-free card models and the bot journal models are
// asserted without a Harness or browser. These payloads are the contract the
// client.js toolview components consume (plan Tasks 9, 24, 26).

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";

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
