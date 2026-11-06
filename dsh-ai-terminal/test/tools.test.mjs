// Tests of the Host tool bridge (plan Task 9): one Harness tool per Go tool,
// upstream abort on the caller signal, a clear tool error when the backend is
// down, and the card metadata of the invoke replies.
//
//   node --test dsh-ai-terminal/test/

import assert from 'node:assert/strict';
import http from 'node:http';
import { test } from 'node:test';

import {
  buildToolDefinition,
  createToolBridge,
  normalizeToolInput,
  renderToolResult,
  toolPresentationMeta,
} from '../tools.js';

// --- helpers ---------------------------------------------------------------

/** Listen on an ephemeral loopback port and resolve it. */
function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(server.address().port));
  });
}

/** Close a server, dropping keep-alive connections first. */
function close(server) {
  return new Promise((resolve) => {
    if (typeof server.closeAllConnections === 'function') server.closeAllConnections();
    server.close(() => resolve());
  });
}

/** Read the whole request body. */
function readBody(req) {
  return new Promise((resolve) => {
    let body = '';
    req.setEncoding('utf8');
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => resolve(body));
  });
}

/** Send one JSON reply. */
function sendJSON(res, status, body) {
  const data = JSON.stringify(body);
  res.writeHead(status, { 'content-type': 'application/json', 'content-length': Buffer.byteLength(data) });
  res.end(data);
}

/** Minimal fake of the Harness tool registry. */
function fakeTools() {
  const registrations = [];
  let disposed = 0;
  return {
    registrations,
    disposed: () => disposed,
    register(definition) {
      registrations.push(definition);
      return () => { disposed += 1; };
    },
  };
}

/** One Go ToolInfo fixture (shape of GET /api/agent/tools). */
function toolInfo(name, input) {
  return { name, title: name, description: 'Описание ' + name, input };
}

const GO_QUOTE_INPUT = {
  type: 'object',
  properties: {
    symbol: { type: 'string', description: 'Тикер' },
  },
  required: ['symbol'],
  additionalProperties: false,
};

/** Build a bridge over one HTTP server with the fake registry. */
async function bridgeOver(handler, token = 'test-token') {
  const server = http.createServer(handler);
  const port = await listen(server);
  const tools = fakeTools();
  const bridge = createToolBridge({ tools, port, token, log: () => {} });
  return { server, port, tools, bridge };
}

// --- normalizer ------------------------------------------------------------

test('normalizes the Go JSON Schema to the registry subset', () => {
  const warnings = [];
  const input = {
    type: 'object',
    properties: {
      symbol: { type: 'string', description: 'Тикер', format: 'ticker' },
      side: { type: 'string', enum: ['buy', 'sell'] },
      params: {
        type: 'object',
        description: 'Параметры',
        additionalProperties: { type: 'number' },
      },
      symbols: { type: 'array', items: { type: 'string' } },
      qty: { type: 'integer' },
    },
    required: ['symbol', 'unknownField'],
    additionalProperties: false,
  };
  const schema = normalizeToolInput(input, (message) => warnings.push(message));
  assert.equal(schema.type, 'object');
  assert.equal(schema.additionalProperties, false);
  assert.deepEqual(schema.required, ['symbol']);
  assert.deepEqual(schema.properties.side.enum, ['buy', 'sell']);
  assert.deepEqual(schema.properties.symbols.items, { type: 'string' });
  assert.equal(schema.properties.params.additionalProperties, true);
  assert.equal(schema.properties.params.type, 'object');
  assert.ok(!('format' in schema.properties.symbol), 'unsupported keyword must be dropped');
  assert.ok(warnings.some((line) => line.includes('format')), 'a drop must be warned about');
});

test('normalizer accepts an invalid input as an open object', () => {
  const warnings = [];
  const schema = normalizeToolInput('not json', (message) => warnings.push(message));
  assert.equal(schema.type, 'object');
  assert.equal(schema.additionalProperties, true);
  assert.ok(warnings.length > 0);
});

// --- registration ----------------------------------------------------------

test('registers one tool per backend tool', async (t) => {
  const listed = [
    toolInfo('get_quote', GO_QUOTE_INPUT),
    toolInfo('list_instruments', { type: 'object', properties: {}, additionalProperties: false }),
    toolInfo('run_backtest', {
      type: 'object',
      properties: { symbol: { type: 'string' }, strategy: { type: 'string' } },
      required: ['symbol', 'strategy'],
      additionalProperties: false,
    }),
  ];
  const { server, bridge, tools } = await bridgeOver((req, res) => {
    if (req.method === 'GET' && req.url === '/api/agent/tools') {
      sendJSON(res, 200, { tools: listed });
      return;
    }
    sendJSON(res, 404, { error: { code: 'not_found', message: 'no route' } });
  });
  t.after(() => close(server));

  const count = await bridge.refresh();
  assert.equal(count, listed.length);
  assert.equal(tools.registrations.length, listed.length);
  const names = tools.registrations.map((definition) => definition.name).sort();
  assert.deepEqual(names, ['terminal_get_quote', 'terminal_list_instruments', 'terminal_run_backtest']);
  for (const definition of tools.registrations) {
    assert.equal(typeof definition.execute, 'function');
    assert.equal(typeof definition.output.render, 'function');
    assert.equal(typeof definition.output.presentationMeta, 'function');
    assert.equal(typeof definition.output.schema, 'object');
    assert.equal(definition.parameters.type, 'object');
    assert.equal(typeof definition.parameters.additionalProperties, 'boolean');
  }

  // A second refresh replaces the previous registrations instead of failing
  // with a duplicate tool name.
  await bridge.refresh();
  assert.equal(tools.disposed(), listed.length);
  assert.equal(tools.registrations.length, listed.length * 2);
});

test('refresh is a no-op without the Harness tool service', async () => {
  const bridge = createToolBridge({ tools: undefined, port: 1, token: 't' });
  assert.equal(await bridge.refresh(), 0);
  bridge.close();
});

// --- execute / abort / backend down ---------------------------------------

test('tool bridge aborts upstream on signal', async (t) => {
  let sawRequest = false;
  let upstreamClosed = false;
  let requestHeaders = null;
  let requestBody = '';
  const { server, bridge, tools } = await bridgeOver(async (req, res) => {
    if (req.method === 'GET' && req.url === '/api/agent/tools') {
      sendJSON(res, 200, { tools: [toolInfo('get_quote', GO_QUOTE_INPUT)] });
      return;
    }
    if (req.method === 'POST' && req.url === '/api/agent/tools/get_quote/invoke') {
      sawRequest = true;
      requestHeaders = req.headers;
      res.on('close', () => { upstreamClosed = true; });
      req.on('aborted', () => { upstreamClosed = true; });
      requestBody = await readBody(req);
      return; // Never reply: the caller aborts the request.
    }
    sendJSON(res, 404, { error: { code: 'not_found', message: 'no route' } });
  });
  t.after(() => close(server));

  await bridge.refresh();
  const definition = tools.registrations[0];
  const controller = new AbortController();
  const pending = definition.execute({ symbol: 'TQBR:SBER' }, { signal: controller.signal });
  setTimeout(() => controller.abort(), 50);
  await assert.rejects(pending, /отмен/);
  assert.ok(sawRequest, 'the bridge must reach the backend before the abort');
  assert.equal(requestHeaders['x-terminal-token'], 'test-token');
  assert.equal(requestHeaders['content-type'], 'application/json');
  assert.deepEqual(JSON.parse(requestBody), { args: { symbol: 'TQBR:SBER' } });
  const deadline = Date.now() + 2000;
  while (!upstreamClosed && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 20));
  assert.ok(upstreamClosed, 'the aborted fetch must close the upstream request');
});

test('backend down yields tool error text', async (t) => {
  const { server, bridge, tools } = await bridgeOver((req, res) => {
    if (req.method === 'GET' && req.url === '/api/agent/tools') {
      sendJSON(res, 200, { tools: [toolInfo('get_quote', GO_QUOTE_INPUT)] });
      return;
    }
    sendJSON(res, 500, { error: { code: 'internal', message: 'boom' } });
  });
  await bridge.refresh();
  const definition = tools.registrations[0];
  await close(server);
  await assert.rejects(definition.execute({ symbol: 'TQBR:SBER' }, {}), /недоступен/);
  await assert.rejects(bridge.refresh(), /недоступен/);
  bridge.close();
});

test('a tool-level error from the backend rejects the tool call', async (t) => {
  const { server, bridge, tools } = await bridgeOver((req, res) => {
    if (req.method === 'GET' && req.url === '/api/agent/tools') {
      sendJSON(res, 200, { tools: [toolInfo('get_quote', GO_QUOTE_INPUT)] });
      return;
    }
    sendJSON(res, 200, { error: 'agent: no quote for TQBR:NOPE' });
  });
  t.after(() => close(server));
  await bridge.refresh();
  await assert.rejects(tools.registrations[0].execute({ symbol: 'TQBR:NOPE' }, {}), /no quote/);
});

test('render and card metadata of a proposal, a backtest and a quote reply', () => {
  const proposal = {
    proposal: {
      id: 'prop_000001', tool: 'place_order', status: 'pending',
      summary: 'купить 1 TQBR:SBER', expiresAt: '2026-11-06T00:10:00Z',
    },
  };
  const proposalText = renderToolResult({}, proposal)[0].text;
  assert.match(proposalText, /Требуется подтверждение/);
  assert.match(proposalText, /prop_000001/);
  const proposalMeta = toolPresentationMeta({}, proposal);
  assert.equal(proposalMeta.kind, 'proposal');
  assert.equal(proposalMeta.proposal.status, 'pending');

  const equity = Array.from({ length: 200 }, (_, index) => ({ t: index, value: 1000 + index }));
  const backtest = {
    result: { symbol: 'TQBR:SBER' },
    artifact: {
      kind: 'backtest',
      payload: {
        symbol: 'TQBR:SBER', strategy: 'sma_cross', periodSec: 300, params: {},
        metrics: { totalReturnPct: 7.5, maxDrawdownPct: 2.1, trades: 3 },
        equity,
        warnings: [],
      },
    },
  };
  assert.match(renderToolResult({}, backtest)[0].text, /доходность/);
  const backtestMeta = toolPresentationMeta({}, backtest);
  assert.equal(backtestMeta.kind, 'backtest');
  assert.equal(backtestMeta.equity.length, 60);
  assert.equal(backtestMeta.metrics.trades, 3);

  const quote = { result: { symbol: 'TQBR:SBER', last: 293.22, bid: 293.2, ask: 293.26, changePct: 0.36 } };
  const quoteMeta = toolPresentationMeta({}, quote);
  assert.equal(quoteMeta.kind, 'quote');
  assert.equal(quoteMeta.last, 293.22);
});

test('buildToolDefinition keeps the Go tool contract', () => {
  const bridge = { warn: () => {}, invoke: async () => ({ result: { ok: true } }) };
  const definition = buildToolDefinition(toolInfo('get_quote', GO_QUOTE_INPUT), bridge);
  assert.equal(definition.name, 'terminal_get_quote');
  assert.equal(definition.description, 'Описание get_quote');
  assert.deepEqual(definition.parameters.required, ['symbol']);
  assert.equal(definition.parameters.additionalProperties, false);
});
