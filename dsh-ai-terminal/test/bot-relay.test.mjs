// Tests of the Host bot relay (plan Task 26): significant SSE events reach the
// session agent created by the terminal_bot_* tools, closed bars stay out, the
// hourly limit batches into one summary, a backend restart resumes from the
// journal without duplicates, and the kill switch stops all bots.

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { createBotRelay } from '../bot-relay.js';
import { killSwitchVisible, parseBotFromReply } from '../bot-ui.js';

/** Fake SSE transport with a controllable event feed. */
function fakeTransport() {
  const state = {
    handlers: null,
    subscriptions: 0,
    stopped: 0,
    bots: [],
    journals: new Map(),
    journalCalls: [],
    stopAllCalls: 0,
  };
  const transport = {
    subscribe(handlers) {
      state.handlers = handlers;
      state.subscriptions += 1;
      return () => { state.stopped += 1; };
    },
    async fetchBots() { return state.bots; },
    async fetchJournal(botId, afterSeq, limit) {
      state.journalCalls.push({ botId, afterSeq, limit });
      const entries = state.journals.get(botId) || [];
      return entries.filter((entry) => entry.seq > afterSeq);
    },
    async stopAll() { state.stopAllCalls += 1; return { stopped: true }; },
  };
  return {
    transport,
    state,
    emit(event) { state.handlers.onEvent(event); },
    status(value) { state.handlers.onStatus(value); },
  };
}

/** Fake agents service: one handle per mapped session id. */
function fakeAgents() {
  const injected = [];
  return {
    injected,
    get(sessionId) {
      return {
        agent: {
          inject(message) { injected.push({ sessionId, message }); },
        },
      };
    },
  };
}

/** Build one relay over the fakes and map bot-1 to sess-1. */
function makeRelay(overrides = {}) {
  const fake = overrides.fake || fakeTransport();
  const agents = overrides.agents || fakeAgents();
  const relay = createBotRelay({
    agents,
    sessionController: overrides.sessionController,
    transport: fake.transport,
    maxEventsPerHour: overrides.maxEventsPerHour || 12,
    summaryBatch: overrides.summaryBatch || 3,
    log: () => {},
    now: overrides.now || (() => 1_000_000),
  });
  relay.trackToolResult(
    'bot_create_from_backtest',
    {},
    { agent: { id: 'sess-1' } },
    { result: { bot: { id: 'bot-1', symbol: 'TQBR:SBER', periodSec: 86400, stage: 'check', qty: 10 } } },
  );
  return { relay, fake, agents };
}

/** One SSE bot event. */
function botEvent(seq, kind, extra = {}) {
  return { type: 'bot', data: { botId: 'bot-1', entry: { seq, kind, signal: 'none', ...extra } } };
}

test('relay delivers signal events to the bot session agent', () => {
  const { relay, fake, agents } = makeRelay();
  relay.start();
  fake.status('open');
  fake.emit(botEvent(1, 'signal', { signal: 'entry', message: 'Вход по SBER: RSI 0,19 < 0,2' }));
  assert.equal(agents.injected.length, 1);
  const text = agents.injected[0].message.content[0].text;
  assert.match(text, /SBER/);
  assert.match(text, /Вход по SBER/);
  assert.equal(agents.injected[0].sessionId, 'sess-1');
});

test('relay keeps closed_bar out of the agent', () => {
  const { relay, fake, agents } = makeRelay();
  relay.start();
  fake.status('open');
  fake.emit(botEvent(1, 'closed_bar', { close: 293.25, signal: 'none', indicators: { rsi: 0.42 } }));
  assert.equal(agents.injected.length, 0, 'a quiet closed bar must not spend tokens');
  fake.emit(botEvent(2, 'closed_bar', { close: 293.30, signal: 'entry', indicators: { rsi: 0.19 } }));
  assert.equal(agents.injected.length, 1, 'a closed bar with a signal is significant');
});

test('relay batches events above the hourly limit into one summary', () => {
  const { relay, fake, agents } = makeRelay({ maxEventsPerHour: 2, summaryBatch: 2 });
  relay.start();
  fake.status('open');
  for (let seq = 1; seq <= 5; seq += 1) {
    fake.emit(botEvent(seq, 'signal', { signal: 'entry', message: 'сигнал ' + seq }));
  }
  // Two direct deliveries, then the third and fourth batch into one summary;
  // the fifth stays buffered until the batch fills.
  assert.equal(agents.injected.length, 3);
  assert.match(agents.injected[0].message.content[0].text, /сигнал 1/);
  assert.match(agents.injected[1].message.content[0].text, /сигнал 2/);
  assert.match(agents.injected[2].message.content[0].text, /Сводка/);
  assert.match(agents.injected[2].message.content[0].text, /сигнал 3/);
  assert.match(agents.injected[2].message.content[0].text, /сигнал 4/);
});

test('relay resubscribes after backend restart and resumes from last seq', async () => {
  const { relay, fake, agents } = makeRelay();
  fake.state.bots = [{ id: 'bot-1', symbol: 'TQBR:SBER', stage: 'dry_run' }];
  fake.state.journals.set('bot-1', [
    { seq: 1, kind: 'signal', signal: 'entry', message: 'старый сигнал' },
    { seq: 2, kind: 'signal', signal: 'entry', message: 'пропущенный сигнал' },
  ]);
  relay.start();
  fake.status('open');
  fake.emit(botEvent(1, 'signal', { signal: 'entry', message: 'старый сигнал' }));
  assert.equal(agents.injected.length, 1);
  // The connection drops, the backend restarts: the journal resumes from seq 1.
  fake.status('closed');
  fake.status('open');
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.deepEqual(fake.state.journalCalls, [{ botId: 'bot-1', afterSeq: 1, limit: 200 }]);
  assert.equal(agents.injected.length, 2, 'only the missed seq 2 is delivered, seq 1 is not duplicated');
  assert.match(agents.injected[1].message.content[0].text, /пропущенный сигнал/);
});

test('kill switch action calls stop-all and hides when no bot runs', async () => {
  const { relay, fake } = makeRelay();
  assert.equal(killSwitchVisible([]), false);
  assert.equal(killSwitchVisible([{ id: 'bot-1', stage: 'stopped' }]), false);
  assert.equal(killSwitchVisible([{ id: 'bot-1', stage: 'stopped' }, { id: 'bot-2', stage: 'dry_run' }]), true);
  await relay.stopAll();
  assert.equal(fake.state.stopAllCalls, 1);
  assert.equal(parseBotFromReply({ result: { bot: { id: 'bot-9', symbol: 'TQBR:GAZP', stage: 'dry_run' } } }).symbol, 'TQBR:GAZP');
});
