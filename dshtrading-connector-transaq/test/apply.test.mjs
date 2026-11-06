import test from 'node:test';
import assert from 'node:assert/strict';
import { startFakeGateway } from './fake-gateway.mjs';
import * as entry from '../index.js';

const INST = { instruments: [{ symbol: 'TQBR:SBER', secCode: 'SBER', board: 'TQBR', name: 'Sber', shortName: 'Sber', decimals: 2, lotSize: 10, minStep: '0.01' }] };

function makeCtx({ services = {}, tools, logger } = {}) {
  const disposers = [];
  const ctx = {
    get: (k) => services[k],
    effect: (fn) => { const d = fn(); if (typeof d === 'function') disposers.push(d); },
    ...(tools ? { tools } : {}),
    ...(logger ? { logger } : {}),
  };
  return { ctx, disposers };
}

function registry() {
  const regs = [];
  return { regs, register: (market, provider, service) => { regs.push({ market, provider, service }); return () => { regs.length = 0; }; } };
}

test('exports name and empty inject', () => {
  assert.equal(entry.name, 'dsh-trading-ru-connector-transaq');
  assert.deepEqual(entry.inject, []);
});

test('registers ru/finam in both registries and disposes on unload', () => {
  const md = registry(); const tr = registry();
  const { ctx, disposers } = makeCtx({ services: { tradingMarketDataRegistry: md, tradingTradeRegistry: tr } });
  entry.apply(ctx, {});
  assert.equal(md.regs.length, 1);
  assert.equal(md.regs[0].market, 'ru');
  assert.equal(md.regs[0].provider, 'finam');
  assert.equal(typeof md.regs[0].service.getTicker, 'function');
  assert.equal(tr.regs.length, 1);
  assert.equal(tr.regs[0].market, 'ru');
  assert.equal(tr.regs[0].provider, 'finam');
  disposers.forEach((d) => d());
  assert.equal(md.regs.length, 0);
  assert.equal(tr.regs.length, 0);
});

test('enabled:false registers nothing', () => {
  const md = registry(); const registered = [];
  const { ctx } = makeCtx({ services: { tradingMarketDataRegistry: md }, tools: { register: (d) => registered.push(d) } });
  entry.apply(ctx, { enabled: false });
  assert.equal(md.regs.length, 0);
  assert.equal(registered.length, 0);
});

test('missing registries still registers tools and disposes them', () => {
  const registered = []; let disposed = 0;
  const { ctx, disposers } = makeCtx({ tools: { register: (d) => { registered.push(d.name); return () => { disposed++; }; } } });
  entry.apply(ctx, undefined);
  assert.ok(registered.includes('ru_get_ticker'));
  assert.equal(disposers.length, registered.length);
  disposers.forEach((d) => d());
  assert.equal(disposed, registered.length);
});

test('tools.register returning nothing is fine; works without ctx.effect', () => {
  const registered = [];
  entry.apply({ get: () => undefined, tools: { register: (d) => { registered.push(d.name); } } }, {});
  assert.ok(registered.length > 0);
});

test('nothing to register warns once and does not throw (with and without logger)', () => {
  const warns = [];
  const logger = (scope) => ({ warn: (m) => warns.push([scope, m]) });
  const { ctx } = makeCtx({ logger });
  entry.apply(ctx, {});
  assert.deepEqual(warns, [['transaq', 'transaq connector: no dsh-trading registries or tools service in this host']]);
  const { ctx: bare } = makeCtx();
  assert.doesNotThrow(() => entry.apply(bare, {}));
});

test('bad gatewayUrl propagates', () => {
  const { ctx } = makeCtx();
  assert.throws(() => entry.apply(ctx, { gatewayUrl: 'http://example.com' }), TypeError);
});

test('token resolved per request from env; credentials service preferred; throwing service falls back', async () => {
  const gw = await startFakeGateway({ token: 'one', routes: { '/api/gw/v1/instruments': INST } });
  const ref = 'TXC_TEST_TOKEN_7';
  const tools = new Map();
  const services = {};
  const { ctx } = makeCtx({ services, tools: { register: (d) => { tools.set(d.name, d); } } });
  try {
    process.env[ref] = 'one';
    entry.apply(ctx, { gatewayUrl: gw.url, tokenRef: ref });
    const list = tools.get('ru_list_instruments');
    await list.execute({});
    gw.setToken('two');
    process.env[ref] = 'two';
    await list.execute({});
    // credentials service wins over env
    gw.setToken('three');
    services.credentials = { resolve: (r) => { assert.equal(r, ref); return { value: 'three' }; } };
    await list.execute({});
    // throwing service falls back to env
    gw.setToken('two');
    services.credentials = { resolve: () => { throw new Error('boom'); } };
    await list.execute({});
    // async credentials service is awaited
    gw.setToken('four');
    services.credentials = { resolve: async () => ({ value: 'four' }) };
    const r4 = await list.execute({});
    assert.equal(JSON.stringify(r4).includes('TRADING_AUTH_FAILED'), false, 'async credentials not awaited');
    assert.ok(gw.calls.length > 0);
    // empty value falls back to env
    gw.setToken('two');
    services.credentials = { resolve: () => ({ value: '' }) };
    await list.execute({});
  } finally { await gw.close(); delete process.env[ref]; }
});
