// tools.js — Host half of the AI Terminal plugin: bridges the Go terminal tools
// into the Harness tool registry (plan Task 9).
//
// The Go backend stays the only executor: every ToolInfo of the agent catalog
// becomes a Harness tool named terminal_<name>, invoked through
// POST /api/agent/tools/{name}/invoke. The mutating tools still pass the human
// approval gate, so a reply may carry a proposal instead of an executed result.
//
// We build plain ToolDefinition objects instead of importing
// @deepseek-ai/dsh-tools: the registry accepts a raw JSON Schema in
// "parameters" plus output {schema, render, presentationMeta}, and defineTool is
// only the author-DSL sugar. This is the documented fallback of the plan and
// avoids depending on the SDK package from a local plugin bundle
// (docs/ai-terminal/dsh-integration.md §2.1).
//
// Go emits JSON Schema in the raw subset the registry accepts, except for
// schema-valued additionalProperties (a typed map), which normalizeToolInput
// relaxes to true: the Go layer validates the argument values anyway.

import { botRenderText, botToolCardModel } from './bot-ui.js';

const TOOL_PREFIX = 'terminal_';
const REQUEST_TIMEOUT_MS = 120_000;
const MAX_RENDER_CHARS = 2000;
const MAX_EQUITY_POINTS = 60;

const SUPPORTED_TYPES = new Set([
  'object', 'array', 'string', 'number', 'integer', 'boolean', 'null',
]);
const ANNOTATION_KEYS = ['description', 'title', 'default', 'examples'];
const SCHEMA_KEYS = new Set([
  'type', 'oneOf', 'properties', 'required', 'additionalProperties', 'items',
  'enum', 'const', 'description', 'title', 'default', 'examples',
]);

/** Whether the value is a plain JSON object. */
function isRecord(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Copy the author annotation keys of one schema node. */
function copyAnnotations(source, target) {
  for (const key of ANNOTATION_KEYS) {
    if (Object.hasOwn(source, key)) target[key] = source[key];
  }
}

/** Relax a schema-valued additionalProperties (a typed map) to the open form. */
function normalizeAdditionalProperties(value, path, warn) {
  if (value === undefined) return false;
  if (typeof value === 'boolean') return value;
  // The raw subset has no schema-valued additionalProperties; the Go layer
  // still validates the values, so the model-facing schema is opened.
  warn(path + ': schema-valued additionalProperties relaxed to true');
  return true;
}

/** Normalize one JSON Schema node to the raw subset accepted by the registry. */
function normalizeSchemaNode(node, path, warn) {
  if (!isRecord(node)) {
    warn(path + ': schema node is not an object; relaxed to any JSON');
    return {};
  }
  for (const key of Object.keys(node)) {
    if (!SCHEMA_KEYS.has(key)) warn(path + ': unsupported schema keyword ' + JSON.stringify(key) + ' dropped');
  }
  if (Array.isArray(node.oneOf)) {
    if (node.oneOf.length >= 2) {
      const out = { oneOf: node.oneOf.map((branch, index) => normalizeSchemaNode(branch, path + '.oneOf[' + index + ']', warn)) };
      copyAnnotations(node, out);
      return out;
    }
    warn(path + ': oneOf needs at least two branches; relaxed to any JSON');
    return {};
  }
  const type = typeof node.type === 'string' ? node.type : undefined;
  if (type === undefined || !SUPPORTED_TYPES.has(type)) {
    if (type !== undefined) warn(path + ': unsupported schema type ' + JSON.stringify(type) + '; relaxed to any JSON');
    const relaxed = {};
    copyAnnotations(node, relaxed);
    return relaxed;
  }
  const out = { type };
  copyAnnotations(node, out);
  switch (type) {
    case 'object': {
      out.properties = {};
      if (isRecord(node.properties)) {
        for (const [key, value] of Object.entries(node.properties)) {
          out.properties[key] = normalizeSchemaNode(value, path + '.' + key, warn);
        }
      }
      if (Array.isArray(node.required)) {
        const required = node.required.filter((name) => typeof name === 'string' && Object.hasOwn(out.properties, name));
        if (required.length > 0) out.required = required;
      }
      out.additionalProperties = normalizeAdditionalProperties(node.additionalProperties, path, warn);
      break;
    }
    case 'array':
      out.items = node.items === undefined ? {} : normalizeSchemaNode(node.items, path + '.items', warn);
      break;
    default:
      if (Array.isArray(node.enum)) {
        const scalar = node.enum.filter((value) => ['string', 'number', 'boolean'].includes(typeof value) || value === null);
        if (scalar.length > 0) out.enum = scalar;
      }
      if (Object.hasOwn(node, 'const')) out.const = node.const;
      break;
  }
  return out;
}

/**
 * Normalize the Go tool input schema (JSON Schema) to the raw subset the Harness
 * tool registry accepts. The result always has an object root.
 */
export function normalizeToolInput(input, warn = () => {}) {
  let schema = input;
  if (typeof schema === 'string') {
    try {
      schema = JSON.parse(schema);
    } catch (error) {
      warn('tool input is not valid JSON: ' + (error && error.message));
      schema = {};
    }
  }
  const normalized = normalizeSchemaNode(schema, 'parameters', warn);
  if (!isRecord(normalized) || normalized.type !== 'object') {
    warn('tool input root is not an object; relaxed to an open object');
    return { type: 'object', properties: {}, additionalProperties: true };
  }
  return normalized;
}

/** Downsample an equity curve for the chat card. */
function downsample(points, limit = MAX_EQUITY_POINTS) {
  if (!Array.isArray(points) || points.length === 0) return [];
  if (points.length <= limit) return points;
  const step = (points.length - 1) / (limit - 1);
  const out = [];
  for (let index = 0; index < limit; index += 1) {
    out.push(points[Math.round(index * step)]);
  }
  return out;
}

/** One-line preview of an arbitrary tool result. */
function previewText(value) {
  if (value === undefined || value === null) return '';
  let text;
  try {
    text = JSON.stringify(value);
  } catch {
    text = String(value);
  }
  if (!text) return '';
  return text.length > MAX_RENDER_CHARS ? text.slice(0, MAX_RENDER_CHARS) + '…' : text;
}

/** Compact metrics object for the backtest card. */
function compactMetrics(metrics) {
  if (!isRecord(metrics)) return null;
  const keys = [
    'totalReturnPct', 'benchmarkReturnPct', 'maxDrawdownPct', 'sharpe',
    'winRatePct', 'profitFactor', 'trades', 'bars',
  ];
  const out = {};
  for (const key of keys) {
    if (typeof metrics[key] === 'number') out[key] = metrics[key];
  }
  return out;
}

/**
 * Build the model-facing content blocks of one invoke reply. The mutating tool
 * proposal is rendered as a text instruction: the model must not assume the
 * action ran.
 */
export function renderToolResult(args, value, toolName = '') {
  if (typeof toolName === 'string' && toolName.startsWith('bot_')) {
    return [{ type: 'text', text: botRenderText(toolName, args, value) }];
  }
  const reply = isRecord(value) ? value : {};
  if (isRecord(reply.proposal)) {
    const proposal = reply.proposal;
    const summary = typeof proposal.summary === 'string' && proposal.summary ? proposal.summary : String(proposal.tool || 'действие');
    return [{ type: 'text', text: 'Требуется подтверждение человека: ' + summary + ' (id ' + String(proposal.id || '') + ', статус ' + String(proposal.status || '') + '). Действие ещё не выполнено.' }];
  }
  if (isRecord(reply.artifact) && reply.artifact.kind === 'backtest' && isRecord(reply.artifact.payload)) {
    const result = reply.artifact.payload;
    const metrics = isRecord(result.metrics) ? result.metrics : {};
    const parts = ['Бэктест ' + String(result.strategy || '') + ' на ' + String(result.symbol || '') + ':'];
    if (typeof metrics.totalReturnPct === 'number') parts.push('доходность ' + metrics.totalReturnPct.toFixed(2) + '%');
    if (typeof metrics.maxDrawdownPct === 'number') parts.push('макс. просадка ' + metrics.maxDrawdownPct.toFixed(2) + '%');
    if (typeof metrics.trades === 'number') parts.push('сделок ' + metrics.trades);
    parts.push('Это бумажная аналитика на реальных данных, не инвестиционная рекомендация.');
    return [{ type: 'text', text: parts.join(' ') }];
  }
  const result = reply.result === undefined ? reply : reply.result;
  return [{ type: 'text', text: previewText(result) || 'Готово.' }];
}

/**
 * Structured data for the chat card: persisted as tool/result.meta by the
 * Harness pipeline and delivered to the tool.call.toolview component as
 * block.meta.
 */
export function toolPresentationMeta(args, value, toolName = '') {
  if (typeof toolName === 'string' && toolName.startsWith('bot_')) {
    const model = botToolCardModel(toolName, args, value);
    if (model !== null) return model;
    return { kind: 'result', preview: previewText(value).slice(0, 600) };
  }
  const reply = isRecord(value) ? value : {};
  if (isRecord(reply.proposal)) return { kind: 'proposal', proposal: reply.proposal };
  if (isRecord(reply.artifact) && reply.artifact.kind === 'backtest' && isRecord(reply.artifact.payload)) {
    const result = reply.artifact.payload;
    return {
      kind: 'backtest',
      symbol: result.symbol,
      strategy: result.strategy,
      periodSec: result.periodSec,
      params: result.params,
      metrics: compactMetrics(result.metrics),
      equity: downsample(result.equity),
      warnings: Array.isArray(result.warnings) ? result.warnings : [],
      tool: toolName,
      // The rules.Spec is the bridge from a rules backtest to a rule bot
      // (plan Task 26): the model passed it in the tool call, so the card can
      // create the bot through the same REST the tools use.
      ...(isRecord(args) && isRecord(args.rules) ? { rules: args.rules } : {}),
    };
  }
  const result = reply.result;
  if (isRecord(result) && typeof result.symbol === 'string'
    && (typeof result.last === 'number' || typeof result.bid === 'number')) {
    return {
      kind: 'quote',
      symbol: result.symbol,
      last: result.last,
      bid: result.bid,
      ask: result.ask,
      changePct: result.changePct,
      time: result.time,
    };
  }
  return { kind: 'result', preview: previewText(result).slice(0, 600) };
}

/** Build one plain ToolDefinition for a Go ToolInfo. */
export function buildToolDefinition(info, bridge) {
  return {
    name: TOOL_PREFIX + info.name,
    description: info.description || info.title || info.name,
    parameters: normalizeToolInput(info.input, bridge.warn),
    output: {
      // The canonical invoke reply is arbitrary JSON: an empty schema accepts
      // any lossless value, and render builds the model-facing text.
      schema: {},
      render: (args, value) => renderToolResult(args, value, info.name),
      presentationMeta: (args, value) => toolPresentationMeta(args, value, info.name),
    },
    async execute(args, exec) {
      const signal = exec && exec.signal;
      const reply = await bridge.invoke(info.name, isRecord(args) ? args : {}, signal);
      // The relay learns which DSH session created a bot from the tool call
      // context (plan Task 26); the hook must never break the tool result.
      if (typeof toolResultHook === 'function') {
        try {
          toolResultHook(info.name, args, exec, reply);
        } catch {
          // A relay bookkeeping failure must not fail the tool call.
        }
      }
      return reply;
    },
  };
}

/** Turn a transport failure into a stable user-facing tool error. */
function transportError(error) {
  if (error && error.name === 'AbortError') return new Error('запрос к терминалу отменён');
  if (error && error.name === 'TimeoutError') return new Error('таймаут запроса к терминалу');
  const message = error && error.message ? error.message : String(error);
  return new Error('бэкенд терминала недоступен: ' + message);
}

/**
 * Create the tool bridge of one plugin instance.
 *
 * options: { tools, port, token, log?, requestTimeoutMs?, fetchImpl? }
 * The bridge is inert when the Harness tool service is absent (tests, smoke
 * runs without Harness): refresh() then does nothing.
 */
export function createToolBridge(options = {}) {
  const tools = options.tools;
  const port = options.port;
  const token = options.token;
  const log = typeof options.log === 'function' ? options.log : () => {};
  const requestTimeoutMs = Number.isInteger(options.requestTimeoutMs) ? options.requestTimeoutMs : REQUEST_TIMEOUT_MS;
  const fetchImpl = options.fetchImpl || globalThis.fetch;
  const baseURL = 'http://127.0.0.1:' + port;
  const state = { disposers: [], seq: 0, closed: false };
  let toolResultHook = null;

  function warn(message) {
    log('warn', message);
  }

  function headers(extra) {
    return { accept: 'application/json', 'x-terminal-token': token, ...extra };
  }

  function hasTools() {
    return isRecord(tools) && typeof tools.register === 'function';
  }

  async function requestJSON(pathname, init = {}, signal) {
    const timeout = AbortSignal.timeout(requestTimeoutMs);
    const combined = signal === undefined || typeof AbortSignal.any !== 'function'
      ? (signal === undefined ? timeout : signal)
      : AbortSignal.any([timeout, signal]);
    let response;
    try {
      response = await fetchImpl(baseURL + pathname, {
        ...init,
        headers: headers(init.headers),
        signal: combined,
      });
    } catch (error) {
      throw transportError(error);
    }
    const text = await response.text();
    let body = null;
    if (text) {
      try {
        body = JSON.parse(text);
      } catch {
        body = null;
      }
    }
    if (!response.ok) {
      const message = isRecord(body) && isRecord(body.error) && typeof body.error.message === 'string'
        ? body.error.message
        : 'HTTP ' + response.status;
      throw new Error(message);
    }
    if (body === null) throw new Error('бэкенд терминала вернул пустой ответ');
    return body;
  }

  async function invoke(name, args, signal) {
    const reply = await requestJSON(
      '/api/agent/tools/' + encodeURIComponent(name) + '/invoke',
      { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ args }) },
      signal,
    );
    if (typeof reply.error === 'string' && reply.error !== '') throw new Error(reply.error);
    return reply;
  }

  function unregister() {
    for (const dispose of state.disposers.reverse()) {
      try {
        dispose();
      } catch {
        // The registry already dropped the registration.
      }
    }
    state.disposers = [];
  }

  async function refresh() {
    if (state.closed || !hasTools()) return 0;
    const seq = ++state.seq;
    const body = await requestJSON('/api/agent/tools');
    if (seq !== state.seq || state.closed) return 0;
    const list = Array.isArray(body.tools) ? body.tools : [];
    unregister();
    for (const info of list) {
      if (!isRecord(info) || typeof info.name !== 'string' || info.name === '') continue;
      try {
        const dispose = tools.register(buildToolDefinition(info, bridge));
        state.disposers.push(typeof dispose === 'function' ? dispose : () => {});
      } catch (error) {
        warn('не удалось зарегистрировать инструмент ' + TOOL_PREFIX + info.name + ': ' + (error && error.message ? error.message : String(error)));
      }
    }
    return state.disposers.length;
  }

  function close() {
    state.closed = true;
    state.seq += 1;
    unregister();
  }

  /** Observe every terminal tool reply (the bot relay maps botId -> session). */
  function setToolResultHook(hook) {
    toolResultHook = typeof hook === 'function' ? hook : null;
  }

  const bridge = { invoke, refresh, unregister, close, warn, port, token, setToolResultHook };
  return bridge;
}
