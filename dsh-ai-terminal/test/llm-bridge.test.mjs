// Tests of the OpenAI-compatible LLM bridge (plan Task 10): the OpenAI tool_calls
// mapping in both directions, the token gate and the stream assembly.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import nodePath from 'node:path';
import { pathToFileURL } from 'node:url';
import { test } from 'node:test';

import { createLLMBridge } from '../llm-bridge.js';
import { createSkillProvider, parseSkillFile } from '../skills.js';

// --- helpers ---------------------------------------------------------------

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(server.address().port));
  });
}

function close(server) {
  return new Promise((resolve) => {
    if (typeof server.closeAllConnections === 'function') server.closeAllConnections();
    server.close(() => resolve());
  });
}

/** Start the bridge over one HTTP server with a fake ctx.llm service. */
async function startBridge(llm, overrides = {}) {
  const bridge = createLLMBridge({ llm, token: 'test-llm-token', provider: 'test-provider', model: 'test-model', ...overrides });
  const server = http.createServer((req, res) => {
    Promise.resolve(bridge.handler(req, res)).catch((error) => {
      res.statusCode = 500;
      res.end(String(error));
    });
  });
  const port = await listen(server);
  return { server, url: 'http://127.0.0.1:' + port + '/ai-terminal-ctl/llm/v1/chat/completions' };
}

/** POST one JSON payload and decode the reply. */
async function postJSON(url, payload, headers = {}) {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json', ...headers },
    body: JSON.stringify(payload),
  });
  const text = await response.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    json = null;
  }
  return { status: response.status, json, text };
}

/** Build a fake ctx.llm whose stream yields the given chunks. */
function fakeLLM(chunks, calls = []) {
  return {
    stream(options) {
      calls.push(options);
      return (async function* generate() {
        for (const chunk of chunks) yield chunk;
      })();
    },
  };
}

// --- tests -----------------------------------------------------------------

test('llm bridge maps tool_calls both ways', async (t) => {
  const calls = [];
  const llm = fakeLLM([
    { type: 'tool-call-delta', index: 0, id: 'call_1', name: 'run_backtest', argumentsDelta: '{"symbol":' },
    { type: 'tool-call-delta', index: 0, argumentsDelta: '"TQBR:SBER"}' },
    { type: 'usage', usage: { inputTokens: 120, outputTokens: 30, totalTokens: 150 } },
    { type: 'finish', reason: { kind: 'tool-calls' } },
  ], calls);
  const { server, url } = await startBridge(llm);
  t.after(() => close(server));

  const { status, json } = await postJSON(url, {
    model: 'deepseek-chat',
    messages: [
      { role: 'system', content: 'Ты аналитик.' },
      { role: 'user', content: 'протестируй SBER' },
      {
        role: 'assistant',
        content: null,
        tool_calls: [{ id: 'call_0', type: 'function', function: { name: 'get_quote', arguments: '{"symbol":"TQBR:SBER"}' } }],
      },
      { role: 'tool', tool_call_id: 'call_0', content: '{"last":293.22}' },
    ],
    tools: [{
      type: 'function',
      function: {
        name: 'run_backtest',
        description: 'Бэктест',
        parameters: { type: 'object', properties: { symbol: { type: 'string' } }, required: ['symbol'] },
      },
    }],
    temperature: 0.2,
    max_tokens: 500,
    stream: false,
  }, { authorization: 'Bearer test-llm-token' });

  assert.equal(status, 200, 'the bridge must accept the valid token');
  assert.equal(json.object, 'chat.completion');
  assert.equal(json.model, 'test-model', 'the configured plugin model wins');
  const choice = json.choices[0];
  assert.equal(choice.finish_reason, 'tool_calls');
  assert.equal(choice.message.role, 'assistant');
  assert.equal(choice.message.content, null);
  assert.equal(choice.message.tool_calls.length, 1);
  assert.equal(choice.message.tool_calls[0].id, 'call_1');
  assert.equal(choice.message.tool_calls[0].type, 'function');
  assert.equal(choice.message.tool_calls[0].function.name, 'run_backtest');
  assert.equal(choice.message.tool_calls[0].function.arguments, '{"symbol":"TQBR:SBER"}');
  assert.deepEqual(json.usage, { prompt_tokens: 120, completion_tokens: 30, total_tokens: 150 });

  // The inbound mapping: OpenAI messages/tools to the Harness call options.
  assert.equal(calls.length, 1);
  const options = calls[0];
  assert.equal(options.provider, 'test-provider');
  assert.equal(options.model, 'test-model');
  assert.equal(options.temperature, 0.2);
  assert.equal(options.maxTokens, 500);
  assert.equal(options.messages.length, 4);
  assert.deepEqual(options.messages[0], { role: 'system', content: [{ type: 'text', text: 'Ты аналитик.' }] });
  assert.deepEqual(options.messages[1], { role: 'user', content: [{ type: 'text', text: 'протестируй SBER' }] });
  const assistant = options.messages[2];
  assert.equal(assistant.role, 'assistant');
  assert.deepEqual(assistant.content, [{ type: 'tool-call', id: 'call_0', name: 'get_quote', arguments: '{"symbol":"TQBR:SBER"}' }]);
  assert.equal(options.messages[3].role, 'tool');
  assert.equal(options.messages[3].toolCallId, 'call_0');
  assert.equal(options.tools.length, 1);
  assert.equal(options.tools[0].name, 'run_backtest');
  assert.equal(options.tools[0].parameters.type, 'object');
});

test('llm bridge maps text and keeps usage mandatory', async (t) => {
  const llm = fakeLLM([
    { type: 'text-delta', index: 0, text: 'Готово' },
    { type: 'text-delta', index: 0, text: ': бэктест выполнен.' },
    { type: 'finish', reason: { kind: 'stop' } },
  ]);
  const { server, url } = await startBridge(llm);
  t.after(() => close(server));

  const { status, json } = await postJSON(url, {
    messages: [{ role: 'user', content: 'привет' }],
  }, { authorization: 'Bearer test-llm-token' });

  assert.equal(status, 200);
  assert.equal(json.choices[0].message.content, 'Готово: бэктест выполнен.');
  assert.equal(json.choices[0].finish_reason, 'stop');
  assert.deepEqual(json.usage, { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 });
});

test('llm bridge rejects missing or wrong token', async (t) => {
  const llm = fakeLLM([{ type: 'finish', reason: { kind: 'stop' } }]);
  const { server, url } = await startBridge(llm);
  t.after(() => close(server));

  const missing = await postJSON(url, { messages: [{ role: 'user', content: 'hi' }] });
  assert.equal(missing.status, 401);
  assert.equal(missing.json.error.code, 'invalid_api_key');

  const wrong = await postJSON(url, { messages: [{ role: 'user', content: 'hi' }] }, { authorization: 'Bearer wrong' });
  assert.equal(wrong.status, 401);
  assert.equal(wrong.json.error.code, 'invalid_api_key');

  const good = await postJSON(url, { messages: [{ role: 'user', content: 'hi' }] }, { authorization: 'Bearer test-llm-token' });
  assert.equal(good.status, 200);
});

test('llm bridge maps an upstream failure to 502', async (t) => {
  const llm = fakeLLM([
    { type: 'finish', reason: { kind: 'error', failure: { class: 'RATE_LIMIT' } } },
  ]);
  const { server, url } = await startBridge(llm);
  t.after(() => close(server));

  const { status, json } = await postJSON(url, {
    messages: [{ role: 'user', content: 'hi' }],
  }, { authorization: 'Bearer test-llm-token' });
  assert.equal(status, 502);
  assert.equal(json.error.type, 'upstream_error');
  assert.match(json.error.message, /RATE_LIMIT/);
});

test('llm bridge rejects an invalid request payload', async (t) => {
  const llm = fakeLLM([{ type: 'finish', reason: { kind: 'stop' } }]);
  const { server, url } = await startBridge(llm);
  t.after(() => close(server));

  const response = await fetch(url, {
    method: 'POST',
    headers: { authorization: 'Bearer test-llm-token' },
    body: '{not json',
  });
  assert.equal(response.status, 400);

  const noMessages = await postJSON(url, { model: 'x' }, { authorization: 'Bearer test-llm-token' });
  assert.equal(noMessages.status, 400);
});

// --- skills provider (plan Task 10) ---------------------------------------

/** Create one SKILL.md file below the temp dir. */
function writeSkill(root, name, frontmatter, body) {
  const dir = nodePath.join(root, name);
  fs.mkdirSync(dir, { recursive: true });
  const header = Object.entries(frontmatter).map(([key, value]) => key + ': ' + value).join('\n');
  fs.writeFileSync(nodePath.join(dir, 'SKILL.md'), '---\n' + header + '\n---\n\n' + body + '\n');
}

test('skills provider lists and loads frontmatter skills', async (t) => {
  const dir = fs.mkdtempSync(nodePath.join(os.tmpdir(), 'ai-terminal-skills-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const repoSkills = nodePath.join(dir, 'repo-skills');
  const dataSkills = nodePath.join(dir, 'data-skills');
  writeSkill(repoSkills, 'trading-terminal', {
    name: 'trading-terminal',
    description: 'Порядок «бэктест → предложение»',
    whenToUse: 'user asks to backtest a strategy',
    'disable-model-invocation': 'false',
  }, '# Навык\n\nТекст навыка.');
  writeSkill(dataSkills, 'bot-patterns', {
    name: 'bot-patterns',
    description: 'Сгенерированные паттерны',
  }, 'Паттерны.');

  const provider = createSkillProvider({ roots: [repoSkills, dataSkills] });
  assert.equal(provider.name, 'ai-terminal');
  const candidates = await provider.list();
  assert.equal(candidates.length, 2);
  const byName = new Map(candidates.map((candidate) => [candidate.name, candidate]));
  const trading = byName.get('trading-terminal');
  assert.equal(trading.description, 'Порядок «бэктест → предложение»');
  assert.equal(trading.whenToUse, 'user asks to backtest a strategy');
  assert.deepEqual(trading.invocation, { modelInvocable: true, userInvocable: true });
  assert.equal(trading.provider, 'ai-terminal');
  assert.equal(byName.get('bot-patterns').invocation.userInvocable, true);

  const skill = await provider.get(trading);
  assert.equal(skill.name, 'trading-terminal');
  assert.equal(skill.provider, 'ai-terminal');
  assert.match(skill.content, /Текст навыка/);
  assert.ok(!skill.content.includes('description:'), 'the frontmatter must not leak into the body');
  assert.equal(skill.resourceBase.kind, 'directory');
});

test('skills provider maps invocation flags and tolerates bad files', async (t) => {
  const dir = fs.mkdtempSync(nodePath.join(os.tmpdir(), 'ai-terminal-skills-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  writeSkill(dir, 'hidden-skill', {
    name: 'hidden-skill',
    description: 'Скрытый от модели навык',
    'disable-model-invocation': 'true',
  }, 'тело');
  // Invalid name and missing description are skipped, not fatal.
  writeSkill(dir, 'bad name', { name: 'bad name', description: 'x' }, 'x');
  fs.mkdirSync(nodePath.join(dir, 'empty-skill'));
  const provider = createSkillProvider({ roots: [dir, nodePath.join(dir, 'missing-root')] });
  const candidates = await provider.list();
  assert.equal(candidates.length, 1);
  assert.deepEqual(candidates[0].invocation, { modelInvocable: false, userInvocable: true });
});

test('skill frontmatter parser keeps the body after the header', () => {
  const parsed = parseSkillFile('---\nname: demo\ndescription: "Описание"\n---\n\n# Тело\n');
  assert.equal(parsed.data.name, 'demo');
  assert.equal(parsed.data.description, 'Описание');
  assert.equal(parsed.content, '# Тело\n');
  assert.equal(parseSkillFile('no frontmatter'), null);
});

// --- client surfaces (plan Task 10) ---------------------------------------

/** Minimal React stub for the Client bundle. */
function fakeReactForClient() {
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

/** Load client.js through a fake module loader. */
async function loadClientModule() {
  let registration;
  globalThis.window = {
    __ModuleLoader__: {
      load(spec) {
        registration = spec;
      },
    },
  };
  const file = nodePath.join(import.meta.dirname, '..', 'client.js');
  await import(pathToFileURL(file).href + '?test=' + Date.now());
  assert.ok(registration, 'client.js must register through window.__ModuleLoader__');
  return registration.factory((name) => {
    if (name === 'react') return fakeReactForClient();
    throw new Error('unexpected require: ' + name);
  });
}

/** Collect the elements carrying an onClick handler. */
function clickableElements(node) {
  const found = [];
  const visit = (value) => {
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (value && typeof value === 'object') {
      if (value.props && typeof value.props.onClick === 'function') found.push(value);
      if (value.children) visit(value.children);
    }
  };
  visit(node);
  return found;
}

test('client surfaces register the tab, header action and selection interaction', async () => {
  const client = await loadClientModule();
  const registered = [];
  const effects = [];
  const tabs = [];
  const interactions = [];
  const ctx = {
    effect(fn) {
      const dispose = fn();
      effects.push(dispose);
      return typeof dispose === 'function' ? dispose : () => {};
    },
    locale: { register() {}, bind: () => (key) => key },
    slots: {
      inject(name, factory) {
        factory();
        return () => {};
      },
      register(spec, Component) {
        registered.push({ spec, Component });
        return () => {};
      },
    },
    sidebarRightTabs: {
      register(definition) {
        tabs.push(definition);
        return () => {};
      },
    },
    uiConversation: {
      interactions: {
        register(definition) {
          interactions.push(definition);
          return () => {};
        },
      },
    },
  };
  client.apply(ctx);

  assert.equal(tabs.length, 1);
  assert.equal(tabs[0].id, 'ai-terminal/terminal');
  assert.equal(tabs[0].kind, 'ai-terminal');
  const names = registered.map((entry) => entry.spec.name);
  assert.ok(names.includes('sidebar.right.pane.tab'));
  assert.ok(names.includes('conversation.session.header.actions'));
  assert.ok(names.includes('conversation.chat.text-interaction'));
  assert.equal(registered.find((entry) => entry.spec.name === 'sidebar.right.pane.tab').spec.key, 'ai-terminal/terminal');
  assert.equal(interactions.length, 1);
  assert.equal(interactions[0].id, 'ai-terminal-ask-selection');

  // Header action inserts the visible chart ScreenContext quote.
  const header = registered.find((entry) => entry.spec.name === 'conversation.session.header.actions').Component;
  const quotes = [];
  const headerElement = header({ inputActions: { insertQuote: (text) => quotes.push(text) } });
  const headerButtons = clickableElements(headerElement);
  assert.equal(headerButtons.length, 1);
  headerButtons[0].props.onClick();
  assert.equal(quotes.length, 1);
  const chartQuote = JSON.parse(quotes[0]);
  assert.equal(chartQuote.source, 'ai-terminal');
  assert.ok(typeof chartQuote.activeTab === 'string');

  // Selected text opens the interaction surface, which inserts a text quote.
  let opened = false;
  interactions[0].onInteraction({
    readText: () => 'выделенный фрагмент',
    open: () => { opened = true; },
  });
  assert.ok(opened, 'the interaction must claim the gesture');
  const surface = registered.find((entry) => entry.spec.name === 'conversation.chat.text-interaction').Component;
  const surfaceElement = surface({ readText: () => 'выделенный фрагмент', dismiss: () => {} });
  clickableElements(surfaceElement)[0].props.onClick();
  assert.equal(quotes.length, 2);
  const selectionQuote = JSON.parse(quotes[1]);
  assert.equal(selectionQuote.source, 'ai-terminal');
  assert.equal(selectionQuote.selection.text, 'выделенный фрагмент');
});
