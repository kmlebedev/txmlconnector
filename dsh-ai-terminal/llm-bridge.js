// llm-bridge.js — OpenAI-compatible bridge to the Harness LLM (plan Task 10).
//
// The Go terminal agent is an OpenAI-compatible client: it POSTs
// /ai-terminal-ctl/llm/v1/chat/completions with its messages and tool schemas.
// The bridge maps that request onto ctx.llm.stream (the user's configured
// Harness providers) and maps the raw chunk stream back to a non-streaming
// OpenAI reply. The backend authenticates with a per-launch one-time token
// (LLM_API_KEY); provider credentials never leave the Harness process.
//
// Mapping notes (docs/ai-terminal/dsh-integration.md §2.2):
// - OpenAI tool_calls[].function.arguments is a JSON string; the Harness
//   message form carries the same JSON string in a tool-call content block.
// - The Harness stream is raw chunks: text-delta, tool-call-delta, usage and a
//   terminal finish; the bridge assembles them itself (no SDK import).
// - The configured provider/model win; the request model is a client default.

import { randomUUID, timingSafeEqual } from 'node:crypto';

const DEFAULT_PROVIDER = 'deepseek-official';
const DEFAULT_MODEL = 'deepseek-flash';
const MAX_BODY_BYTES = 4 * 1024 * 1024;
const MAX_TOKENS_LIMIT = 64_000;

/** Write one JSON reply. */
function sendJSON(res, status, payload) {
  const data = JSON.stringify(payload);
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'cache-control': 'no-store',
    'content-length': Buffer.byteLength(data),
  });
  res.end(data);
}

/** OpenAI-shaped error reply. */
function sendError(res, status, message, code = 'invalid_request_error') {
  sendJSON(res, status, { error: { message, type: 'invalid_request_error', code } });
}

/** Marshal one error into a sanitized OpenAI upstream error. */
function sendUpstreamError(res, error) {
  const message = error && error.message ? String(error.message) : 'LLM upstream failed';
  sendJSON(res, 502, { error: { message: 'LLM upstream error: ' + message, type: 'upstream_error', code: 'upstream_error' } });
}

/** Constant-time bearer check. */
function tokenMatches(header, token) {
  if (typeof header !== 'string' || typeof token !== 'string' || token === '') return false;
  const prefix = 'Bearer ';
  if (!header.startsWith(prefix)) return false;
  const supplied = Buffer.from(header.slice(prefix.length), 'utf8');
  const expected = Buffer.from(token, 'utf8');
  if (supplied.length !== expected.length) return false;
  try {
    return timingSafeEqual(supplied, expected);
  } catch {
    return false;
  }
}

/** Read a bounded request body. */
function readBody(req) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        const error = new Error('request body is too large');
        error.status = 413;
        reject(error);
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

/** Convert OpenAI text content (string or content parts) to Harness blocks. */
function textBlocks(content) {
  if (typeof content === 'string') return content === '' ? [] : [{ type: 'text', text: content }];
  if (!Array.isArray(content)) return [];
  const blocks = [];
  for (const part of content) {
    if (typeof part === 'string') {
      if (part !== '') blocks.push({ type: 'text', text: part });
    } else if (part && part.type === 'text' && typeof part.text === 'string') {
      if (part.text !== '') blocks.push({ type: 'text', text: part.text });
    }
  }
  return blocks;
}

/** Map one OpenAI chat message to the Harness durable message shape. */
export function openAIMessageToDsh(message) {
  if (!message || typeof message !== 'object') return null;
  const role = message.role;
  switch (role) {
    case 'system':
    case 'developer':
      return { role: 'system', content: textBlocks(message.content) };
    case 'user':
      return { role: 'user', content: textBlocks(message.content) };
    case 'assistant': {
      const content = textBlocks(message.content);
      if (Array.isArray(message.tool_calls)) {
        for (const call of message.tool_calls) {
          const fn = call && call.function ? call.function : {};
          if (!fn.name) continue;
          content.push({
            type: 'tool-call',
            id: typeof call.id === 'string' ? call.id : '',
            name: String(fn.name),
            arguments: typeof fn.arguments === 'string' ? fn.arguments : JSON.stringify(fn.arguments || {}),
          });
        }
      }
      return { role: 'assistant', content };
    }
    case 'tool':
      return {
        role: 'tool',
        toolCallId: typeof message.tool_call_id === 'string' ? message.tool_call_id : '',
        content: textBlocks(message.content),
      };
    default:
      return null;
  }
}

/** Map the OpenAI message list to Harness messages. */
export function openAIMessagesToDsh(messages) {
  if (!Array.isArray(messages)) return [];
  const out = [];
  for (const message of messages) {
    const mapped = openAIMessageToDsh(message);
    if (mapped !== null) out.push(mapped);
  }
  return out;
}

/** Map OpenAI function tools to Harness tool schemas. */
export function openAIToolsToDsh(tools) {
  if (!Array.isArray(tools)) return undefined;
  const out = [];
  for (const tool of tools) {
    const fn = tool && tool.type === 'function' && tool.function ? tool.function : null;
    if (!fn || typeof fn.name !== 'string' || fn.name === '') continue;
    out.push({
      name: fn.name,
      description: typeof fn.description === 'string' ? fn.description : '',
      parameters: fn.parameters && typeof fn.parameters === 'object'
        ? fn.parameters
        : { type: 'object', properties: {}, additionalProperties: false },
    });
  }
  return out.length > 0 ? out : undefined;
}

/** Map a Harness finish reason to the OpenAI finish_reason value. */
export function finishReasonToOpenAI(finish, hasToolCalls) {
  const kind = finish && finish.kind ? finish.kind : 'stop';
  switch (kind) {
    case 'tool-calls':
      return 'tool_calls';
    case 'max-tokens':
      return 'length';
    case 'stop':
      return hasToolCalls ? 'tool_calls' : 'stop';
    default:
      return hasToolCalls ? 'tool_calls' : 'stop';
  }
}

/** Map Harness TokenUsage to the OpenAI usage object (always present). */
export function usageToOpenAI(usage) {
  const input = usage && Number.isFinite(usage.inputTokens) ? usage.inputTokens : 0;
  const output = usage && Number.isFinite(usage.outputTokens) ? usage.outputTokens : 0;
  const total = usage && Number.isFinite(usage.totalTokens) ? usage.totalTokens : input + output;
  return { prompt_tokens: input, completion_tokens: output, total_tokens: total };
}

/**
 * Assemble raw Harness chunks into one OpenAI completion message.
 * Exported for the node tests: the assembler is the mapping core.
 */
export async function assembleStream(stream) {
  const texts = new Map();
  const toolCalls = new Map();
  let usage = null;
  let finish = null;
  for await (const chunk of stream) {
    if (!chunk || typeof chunk !== 'object') continue;
    switch (chunk.type) {
      case 'text-delta': {
        const index = Number.isInteger(chunk.index) ? chunk.index : 0;
        texts.set(index, (texts.get(index) || '') + (typeof chunk.text === 'string' ? chunk.text : ''));
        break;
      }
      case 'tool-call-delta': {
        const index = Number.isInteger(chunk.index) ? chunk.index : toolCalls.size;
        let partial = toolCalls.get(index);
        if (!partial) {
          partial = { id: '', name: '', arguments: '' };
          toolCalls.set(index, partial);
        }
        if (typeof chunk.id === 'string' && chunk.id !== '') partial.id = chunk.id;
        if (typeof chunk.name === 'string' && chunk.name !== '') partial.name = chunk.name;
        if (typeof chunk.argumentsDelta === 'string') partial.arguments += chunk.argumentsDelta;
        break;
      }
      case 'usage':
        usage = chunk.usage || usage;
        break;
      case 'finish':
        finish = chunk.reason || finish;
        break;
      default:
        // block-start/block-end/reasoning-delta are not needed for the OpenAI
        // non-streaming reply: text and tool-call deltas carry the payload.
        break;
    }
  }
  const content = [...texts.entries()].sort((a, b) => a[0] - b[0]).map((entry) => entry[1]).join('');
  const calls = [...toolCalls.entries()].sort((a, b) => a[0] - b[0]).map((entry) => entry[1]);
  return { content, toolCalls: calls, usage, finish };
}

/** Build the OpenAI completion reply from the assembled stream. */
export function buildOpenAICompletion(options) {
  const { content, toolCalls, usage, finish, model } = options;
  const message = { role: 'assistant', content: content === '' ? null : content };
  if (toolCalls.length > 0) {
    message.tool_calls = toolCalls.map((call, index) => ({
      id: call.id !== '' ? call.id : 'call_' + index,
      type: 'function',
      function: {
        name: call.name,
        arguments: call.arguments !== '' ? call.arguments : '{}',
      },
    }));
  }
  return {
    id: 'chatcmpl-' + randomUUID(),
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model,
    choices: [{
      index: 0,
      message,
      finish_reason: finishReasonToOpenAI(finish, toolCalls.length > 0),
    }],
    usage: usageToOpenAI(usage),
  };
}

/**
 * Create the LLM bridge of one plugin instance.
 *
 * options: { llm, token, provider?, model?, log? }
 * The handler is registered on the Harness webServer under
 * /ai-terminal-ctl/llm/v1/chat/completions.
 */
export function createLLMBridge(options = {}) {
  const llm = options.llm;
  const token = typeof options.token === 'string' ? options.token : '';
  const provider = options.provider || DEFAULT_PROVIDER;
  const model = options.model || DEFAULT_MODEL;
  const log = typeof options.log === 'function' ? options.log : () => {};

  async function handler(req, res) {
    if (req.method !== 'POST') {
      sendError(res, 405, 'only POST is supported');
      return;
    }
    if (!tokenMatches(req.headers.authorization, token)) {
      sendError(res, 401, 'missing or invalid API key', 'invalid_api_key');
      return;
    }
    let raw;
    try {
      raw = await readBody(req);
    } catch (error) {
      sendError(res, error && error.status ? error.status : 400, error && error.message ? error.message : 'unreadable request body');
      return;
    }
    let request;
    try {
      request = JSON.parse(raw);
    } catch {
      sendError(res, 400, 'request body is not valid JSON');
      return;
    }
    if (!request || typeof request !== 'object' || !Array.isArray(request.messages)) {
      sendError(res, 400, 'messages must be an array');
      return;
    }
    const messages = openAIMessagesToDsh(request.messages);
    if (messages.length === 0) {
      sendError(res, 400, 'messages must contain at least one supported chat message');
      return;
    }
    const streamOptions = { provider, model, messages };
    const tools = openAIToolsToDsh(request.tools);
    if (tools !== undefined) streamOptions.tools = tools;
    if (Number.isFinite(request.temperature)) streamOptions.temperature = request.temperature;
    if (Number.isFinite(request.max_tokens)) {
      streamOptions.maxTokens = Math.max(1, Math.min(MAX_TOKENS_LIMIT, request.max_tokens));
    }
    let assembled;
    try {
      if (!llm || typeof llm.stream !== 'function') throw new Error('the Harness LLM service is unavailable');
      assembled = await assembleStream(llm.stream(streamOptions));
    } catch (error) {
      log('warn', 'LLM bridge stream failed: ' + (error && error.message ? error.message : String(error)));
      sendUpstreamError(res, error);
      return;
    }
    const finishKind = assembled.finish && assembled.finish.kind ? assembled.finish.kind : 'stop';
    if (finishKind === 'error' || finishKind === 'aborted') {
      const failure = assembled.finish && assembled.finish.failure ? assembled.finish.failure : null;
      const detail = failure && (failure.class || failure.code || failure.message) ? String(failure.class || failure.code || failure.message) : finishKind;
      log('warn', 'LLM bridge upstream finish: ' + detail);
      sendJSON(res, 502, { error: { message: 'LLM upstream error: ' + detail, type: 'upstream_error', code: 'upstream_error' } });
      return;
    }
    sendJSON(res, 200, buildOpenAICompletion({ ...assembled, model }));
  }

  return { handler, token, provider, model };
}
