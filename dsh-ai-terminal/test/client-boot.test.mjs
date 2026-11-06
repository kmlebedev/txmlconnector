// Boot regression: the client plugin runs inside the real Cordis client proxy,
// where reading a service property that is not in the plugin's inject throws
// ('cannot get property "x" without inject'). The optional UI services must be
// read with ctx.get(...) instead. A plain fake-ctx test misses this because a
// plain object returns undefined; the Proxy below mimics Cordis.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { test } from 'node:test';
import { pathToFileURL } from 'node:url';

function fakeReact() {
  const createElement = (type, props, ...children) => ({ type, props: props || {}, children: children.flat(Infinity) });
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

async function loadClientModule() {
  let registration;
  globalThis.window = { __ModuleLoader__: { load(spec) { registration = spec; } } };
  const file = path.join(import.meta.dirname, '..', 'client.js');
  await import(pathToFileURL(file).href + '?boot-test=' + Date.now());
  assert.ok(registration, 'client.js must register through window.__ModuleLoader__');
  return registration.factory((name) => {
    if (name === 'react') return fakeReact();
    throw new Error('unexpected require: ' + name);
  });
}

// A strict Cordis-like service context: only injected services are readable as
// properties; everything else throws on property access, while ctx.get() is the
// documented non-throwing probe.
function strictCtx(services) {
  const target = {
    effect(fn) {
      const dispose = fn();
      return typeof dispose === 'function' ? dispose : () => {};
    },
    get(name) { return services[name]; },
    ...services,
  };
  const reserved = new Set(['then', 'prototype']);
  return new Proxy(target, {
    get(obj, prop, receiver) {
      if (typeof prop === 'symbol' || reserved.has(prop) || String(prop).startsWith('_') || /^\d+$/.test(String(prop))) {
        return Reflect.get(obj, prop, receiver);
      }
      if (Reflect.has(obj, prop)) return Reflect.get(obj, prop, receiver);
      throw new Error('cannot get property "' + String(prop) + '" without inject');
    },
  });
}

test('client plugin activates in a strict Cordis context', async () => {
  const client = await loadClientModule();
  const registered = [];
  const ctx = strictCtx({
    slots: {
      inject(name, factory) { factory(); return () => {}; },
      register(spec, Component) { registered.push({ spec, Component }); return () => {}; },
    },
    locale: { register() {}, bind: () => (key) => key },
    uiConversation: { interactions: { register() { return () => {}; } } },
    sidebarRightTabs: { register() { return () => {}; } },
  });
  assert.doesNotThrow(() => client.apply(ctx));
  assert.ok(registered.length > 0, 'the plugin must register at least one slot');
});