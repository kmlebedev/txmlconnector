import assert from 'node:assert/strict';
import { test } from 'node:test';

import { createSymbolResolver, toCanonical, toNative } from '../symbols.js';

const inst = (board, secCode) => ({ symbol: `${board}:${secCode}`, secCode, board, lotSize: 10, decimals: 2 });
const LIST = [inst('TQBR', 'SBER'), inst('TQBR', 'GAZP'), inst('SMAL', 'SBER'), inst('FQBR', 'AAPL'), inst('SMAL', 'AAPL')];

test('toNative / toCanonical', () => {
  assert.equal(toNative('SBER.TQBR'), 'TQBR:SBER');
  assert.equal(toCanonical('TQBR:SBER'), 'SBER.TQBR');
});

test('resolver: SBER → TQBR:SBER, sber.tqbr → TQBR:SBER', async () => {
  const r = createSymbolResolver(async () => LIST);
  const a = await r.resolve('SBER');
  assert.equal(a.native, 'TQBR:SBER');
  assert.equal(a.canonical, 'SBER.TQBR');
  assert.equal(a.instrument.board, 'TQBR');
  assert.equal((await r.resolve('sber.tqbr')).native, 'TQBR:SBER');
  assert.equal((await r.resolve('tqbr:sber')).native, 'TQBR:SBER');
  assert.equal((await r.resolve('GAZP')).native, 'TQBR:GAZP');
});

test('resolver: ambiguous without TQBR lists candidates', async () => {
  const r = createSymbolResolver(async () => LIST);
  await assert.rejects(r.resolve('AAPL'), (e) => {
    assert.equal(e.code, 'TRADING_UNSUPPORTED_SYMBOL');
    assert.match(e.message, /FQBR:AAPL/);
    assert.match(e.message, /SMAL:AAPL/);
    return true;
  });
});

test('resolver: unknown → TRADING_SYMBOL_NOT_FOUND', async () => {
  const r = createSymbolResolver(async () => LIST);
  await assert.rejects(r.resolve('NOPE'), { code: 'TRADING_SYMBOL_NOT_FOUND' });
  await assert.rejects(r.resolve('NOPE.TQBR'), { code: 'TRADING_SYMBOL_NOT_FOUND' });
  await assert.rejects(r.resolve(''), { code: 'TRADING_SYMBOL_NOT_FOUND' });
});

test('resolver: caches 60s and refreshes once on a miss', async () => {
  let loads = 0;
  let now = 1000;
  let list = [inst('TQBR', 'SBER')];
  const r = createSymbolResolver(async () => { loads++; return list; }, { now: () => now });
  await r.resolve('SBER');
  await r.resolve('SBER.TQBR');
  assert.equal(loads, 1);
  list = [inst('TQBR', 'SBER'), inst('TQBR', 'LKOH')];
  assert.equal((await r.resolve('LKOH')).native, 'TQBR:LKOH'); // miss → one refresh
  assert.equal(loads, 2);
  await assert.rejects(r.resolve('NOPE'), { code: 'TRADING_SYMBOL_NOT_FOUND' });
  assert.equal(loads, 3); // exactly one refresh per miss
  now += 61_000;
  await r.resolve('SBER');
  assert.equal(loads, 4); // TTL expiry
});
