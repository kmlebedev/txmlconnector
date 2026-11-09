// node --test dshtrading-connector-transaq/test/

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { DEFAULTS, resolveConfig } from '../config.js';
import { createGatewayClient, TradingError } from '../gateway-client.js';
import { startFakeGateway } from './fake-gateway.mjs';

const SECRET = 'tok-SECRET-123';

test('resolveConfig merges defaults', () => {
  assert.deepEqual(resolveConfig(undefined), DEFAULTS);
  assert.equal(resolveConfig({ timeoutMs: 100 }).timeoutMs, 100);
});

test('resolveConfig rejects non-loopback gatewayUrl', () => {
  for (const url of ['http://example.com:18090', 'https://127.0.0.1:18090', 'http://10.0.0.5', 'nonsense']) {
    assert.throws(() => resolveConfig({ gatewayUrl: url }), TypeError, url);
  }
  for (const url of ['http://127.0.0.1:1', 'http://localhost:2', 'http://[::1]:3']) {
    assert.equal(resolveConfig({ gatewayUrl: url }).gatewayUrl, url);
  }
  assert.throws(() => resolveConfig({ tokenRef: 'bad-name' }), TypeError);
});

test('client sends token and query, skips undefined', async () => {
  const gw = await startFakeGateway({ token: SECRET, routes: { '/api/gw/v1/quotes': { quotes: {} } } });
  try {
    const client = createGatewayClient({ config: resolveConfig({ gatewayUrl: gw.url }), resolveToken: async () => SECRET });
    assert.deepEqual(await client.get('/quotes', { symbols: 'TQBR:SBER', limit: undefined }), { quotes: {} });
    assert.deepEqual(gw.calls, ['/api/gw/v1/quotes?symbols=TQBR%3ASBER']);
  } finally { await gw.close(); }
});

test('client maps 401/404/503/400/timeout to TRADING_* codes', async () => {
  const gw = await startFakeGateway({
    token: SECRET,
    routes: {
      '/api/gw/v1/a': () => ({ status: 404, body: { error: { code: 'not_found', message: 'unknown symbol' } } }),
      '/api/gw/v1/b': () => ({ status: 503, body: { error: { code: 'unavailable', message: 'feed down' } } }),
      '/api/gw/v1/c': () => ({ status: 400, body: { error: { code: 'bad', message: 'bad symbol' } } }),
      '/api/gw/v1/d': () => ({ status: 500, body: 'oops' }),
      '/api/gw/v1/h': () => ({ hang: true }),
    },
  });
  try {
    const config = resolveConfig({ gatewayUrl: gw.url, timeoutMs: 150 });
    const code = async (token, path) => {
      const client = createGatewayClient({ config, resolveToken: () => token });
      try { await client.get(path); } catch (e) {
        assert.ok(e instanceof TradingError);
        assert.equal(e.name, 'TradingServiceError');
        return e.code;
      }
      assert.fail('expected rejection');
    };
    assert.equal(await code('wrong', '/a'), 'TRADING_AUTH_FAILED');
    assert.equal(await code(SECRET, '/a'), 'TRADING_SYMBOL_NOT_FOUND');
    assert.equal(await code(SECRET, '/b'), 'TRADING_UPSTREAM_ERROR');
    assert.equal(await code(SECRET, '/c'), 'TRADING_UNSUPPORTED_SYMBOL');
    assert.equal(await code(SECRET, '/d'), 'TRADING_UPSTREAM_ERROR');
    assert.equal(await code(SECRET, '/h'), 'TRADING_NETWORK');
    // gateway restarted with a new token: auth failure, then recovery with the new one
    gw.setToken('new-token');
    assert.equal(await code(SECRET, '/a'), 'TRADING_AUTH_FAILED');
    assert.equal(await code('new-token', '/a'), 'TRADING_SYMBOL_NOT_FOUND');
  } finally { await gw.close(); }
  // gateway down
  const down = createGatewayClient({ config: resolveConfig({ gatewayUrl: 'http://127.0.0.1:1', timeoutMs: 500 }), resolveToken: () => SECRET });
  await assert.rejects(down.get('/info'), { code: 'TRADING_NETWORK' });
});

test('client error never contains the token', async () => {
  const gw = await startFakeGateway({
    routes: { '/api/gw/v1/x': () => ({ status: 503, body: { error: { code: 'u', message: `bad token ${SECRET} echoed` } } }) },
  });
  try {
    const client = createGatewayClient({ config: resolveConfig({ gatewayUrl: gw.url }), resolveToken: () => SECRET });
    await assert.rejects(client.get('/x'), (e) => {
      assert.ok(!e.message.includes(SECRET));
      assert.ok(!String(e.stack).includes(SECRET));
      return true;
    });
    const failing = createGatewayClient({
      config: resolveConfig({ gatewayUrl: gw.url }),
      resolveToken: () => SECRET,
      fetchImpl: async () => { throw new Error(`boom ${SECRET}`); },
    });
    await assert.rejects(failing.get('/x'), (e) => e.code === 'TRADING_NETWORK' && !e.message.includes(SECRET));
  } finally { await gw.close(); }
});
