// Tests of the Chat tool cards of the AI Terminal plugin (plan Task 9).
//
// client.js is a Harness Client bundle: it calls window.__ModuleLoader__.load
// and requires('react'). The test boots it in Node with a fake module loader and
// a tiny React stub, so the slot registrations and the card elements are
// verified without a live Harness, browser or React renderer.
//
//   node --test dsh-ai-terminal/test/

import assert from 'node:assert/strict';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { test } from 'node:test';

// --- fake React ------------------------------------------------------------

/** Minimal React stub: elements are plain objects, hooks return inert values. */
function fakeReact() {
  const createElement = (type, props, ...children) => ({
    type,
    props: props || {},
    children: children.flat(Infinity),
  });
  return {
    createElement,
    memo: (component) => component,
    useState: (initial) => [typeof initial === 'function' ? initial() : initial, () => {}],
    useMemo: (factory) => factory(),
    useCallback: (callback) => callback,
    useEffect: () => {},
    useRef: () => ({ current: null }),
    Fragment: 'Fragment',
  };
}

// --- plugin bootstrap ------------------------------------------------------

/** Load client.js through a fake module loader and return its module object. */
async function loadClient() {
  let registration;
  globalThis.window = {
    __ModuleLoader__: {
      load(spec) {
        registration = spec;
      },
    },
  };
  const file = path.join(import.meta.dirname, '..', 'client.js');
  await import(pathToFileURL(file).href + '?test=' + Date.now());
  assert.ok(registration, 'client.js must register through window.__ModuleLoader__');
  const client = registration.factory((name) => {
    if (name === 'react') return fakeReact();
    throw new Error('unexpected require: ' + name);
  });
  return client;
}

const client = await loadClient();

/** Fake Cordis client context capturing slots registrations. */
function fakeCtx() {
  const registered = [];
  const injected = [];
  const ctx = {
    effect(fn) {
      const dispose = fn();
      return typeof dispose === 'function' ? dispose : () => {};
    },
    locale: {
      register() {},
      bind: () => (key) => key,
    },
    slots: {
      inject(name, factory) {
        injected.push(name);
        factory();
        return () => {};
      },
      register(spec, Component) {
        registered.push({ spec, Component });
        return () => {};
      },
    },
  };
  return { ctx, registered, injected };
}

/** Apply the plugin once and return the captured registrations. */
function applyPlugin() {
  const { ctx, registered, injected } = fakeCtx();
  client.apply(ctx);
  return { registered, injected };
}

const { registered } = applyPlugin();

/** Find the component registered for one wire tool name. */
function toolView(key) {
  const entry = registered.find((item) => item.spec.name === 'tool.call.toolview' && item.spec.key === key);
  assert.ok(entry, 'missing tool.call.toolview registration for ' + key);
  return entry.Component;
}

/** Collect the rendered string leaves of an element tree. */
function textOf(node) {
  const parts = [];
  const visit = (value) => {
    if (value === null || value === undefined || typeof value === 'boolean') return;
    if (typeof value === 'string' || typeof value === 'number') {
      parts.push(String(value));
      return;
    }
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (typeof value === 'object' && value.children) visit(value.children);
  };
  visit(node);
  return parts.join(' ');
}

/** Collect element types (strings and component functions). */
function typesOf(node) {
  const types = [];
  const visit = (value) => {
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (value && typeof value === 'object') {
      types.push(value.type);
      if (value.children) visit(value.children);
    }
  };
  visit(node);
  return types;
}

/** Collect elements with an onClick handler (the card buttons). */
function buttonsOf(node) {
  const buttons = [];
  const visit = (value) => {
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (value && typeof value === 'object') {
      if (value.props && typeof value.props.onClick === 'function') buttons.push(value);
      if (value.children) visit(value.children);
    }
  };
  visit(node);
  return buttons;
}

/** One backtest card meta fixture. */
function backtestMeta() {
  return {
    kind: 'backtest',
    symbol: 'TQBR:SBER',
    strategy: 'sma_cross',
    metrics: { totalReturnPct: 7.5, maxDrawdownPct: 2.1, trades: 3, sharpe: 1.2 },
    equity: [
      { t: '2026-11-05T10:00:00Z', value: 1000000 },
      { t: '2026-11-05T10:05:00Z', value: 1010000 },
      { t: '2026-11-05T10:10:00Z', value: 1020000 },
    ],
    warnings: [],
  };
}

// --- tests -----------------------------------------------------------------

test('registers the toolview slots of the bridged terminal tools', () => {
  const { registered: entries, injected } = applyPlugin();
  const views = entries.filter((entry) => entry.spec.name === 'tool.call.toolview');
  const keys = views.map((entry) => entry.spec.key);
  for (const key of ['terminal_run_backtest', 'terminal_get_quote', 'terminal_proposal']) {
    assert.ok(keys.includes(key), 'missing plan toolview key ' + key);
  }
  // A proposal is the canonical result of the gated mutating tools: the card is
  // also keyed by their wire names so it renders in a real chat.
  for (const key of ['terminal_place_order', 'terminal_cancel_order', 'terminal_start_autonomy']) {
    const entry = views.find((item) => item.spec.key === key);
    assert.ok(entry, 'missing proposal card key ' + key);
    assert.equal(entry.Component, views.find((item) => item.spec.key === 'terminal_proposal').Component);
  }
  assert.ok(injected.includes('tool.call.toolview'));
  assert.ok(entries.some((entry) => entry.spec.name === 'main'));
  assert.ok(entries.some((entry) => entry.spec.name === 'sidebar.panellist'));
  assert.ok(client.inject.includes('slots'));
  assert.ok(client.inject.includes('locale'));
});

test('backtest card renders metrics and the equity polyline', () => {
  const view = toolView('terminal_run_backtest');
  const element = view({
    phase: 'result',
    toolName: 'terminal_run_backtest',
    callId: 'call_1',
    block: { meta: backtestMeta(), content: [{ type: 'text', text: 'ok' }], isError: false, subCalls: [] },
  });
  const text = textOf(element);
  assert.match(text, /Бэктест/);
  assert.match(text, /sma_cross/);
  assert.match(text, /SBER/);
  assert.match(text, /7,50%/);
  assert.match(text, /2,10%/);
  assert.ok(typesOf(element).includes('polyline'), 'the card must draw the equity curve');
});

test('backtest card shows a waiting state before the result', () => {
  const view = toolView('terminal_run_backtest');
  assert.match(textOf(view({ phase: 'start', block: { name: 'terminal_run_backtest', args: {} } })), /выполняется/);
});

test('quote card renders last, bid and ask', () => {
  const view = toolView('terminal_get_quote');
  const element = view({
    phase: 'result',
    toolName: 'terminal_get_quote',
    callId: 'call_2',
    block: {
      meta: { kind: 'quote', symbol: 'TQBR:SBER', last: 293.22, bid: 293.2, ask: 293.26, changePct: 0.36 },
      content: [],
      isError: false,
      subCalls: [],
    },
  });
  const text = textOf(element);
  assert.match(text, /TQBR:SBER/);
  assert.match(text, /293,22/);
  assert.match(text, /293,20/);
  assert.match(text, /293,26/);
});

test('proposal card posts approve and reports the new status', async () => {
  const view = toolView('terminal_proposal');
  const calls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    calls.push({ url: String(url), init });
    return {
      ok: true,
      status: 200,
      json: async () => ({ proposal: { id: 'prop_000001', status: 'executed' } }),
    };
  };
  try {
    const element = view({
      phase: 'result',
      toolName: 'terminal_proposal',
      callId: 'call_3',
      block: {
        meta: {
          kind: 'proposal',
          proposal: {
            id: 'prop_000001', tool: 'place_order', status: 'pending',
            summary: 'купить 1 TQBR:SBER', expiresAt: '2026-11-06T00:10:00Z',
          },
        },
        content: [],
        isError: false,
        subCalls: [],
      },
    });
    const text = textOf(element);
    assert.match(text, /Требуется подтверждение/);
    assert.match(text, /купить 1 TQBR:SBER/);
    assert.match(text, /pending/);
    const buttons = buttonsOf(element);
    assert.equal(buttons.length, 2, 'the pending proposal needs approve and reject');
    await buttons[0].props.onClick();
    assert.equal(calls.length, 1);
    assert.match(calls[0].url, /\/ai-terminal\/api\/agent\/proposals\/prop_000001\/approve$/);
    assert.equal(calls[0].init.method, 'POST');
    assert.equal(calls[0].init.headers['content-type'], 'application/json');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('proposal card without a result is a waiting state', () => {
  const view = toolView('terminal_proposal');
  assert.match(textOf(view({ phase: 'start', block: { name: 'terminal_proposal', args: {} } })), /Готовлю/);
});
