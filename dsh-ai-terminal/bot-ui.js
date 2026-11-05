// bot-ui.js — pure view-model and formatting helpers of the bot control room
// (plan Task 26). Shared by the Host half (tools.js cards, bot-relay.js agent
// comments) and the node tests.
//
// The browser client bundle of this plugin is a single client.js entry (the
// Harness client-modules loader serves exactly the package's ./client export),
// so the Client half renders these models directly; this module keeps the
// model shape and the Russian wording in one place for Host renderers/tests.

/** Lifecycle stage labels of the rule-bot runtime. */
export const BOT_STAGE_LABELS = {
  check: 'проверка',
  dry_run: 'dry-run',
  paper: 'бумага',
  stopped: 'остановлен',
};

/** Journal kinds of the rule-bot runtime. */
export const BOT_KIND_LABELS = {
  closed_bar: 'свеча',
  signal: 'сигнал',
  risk_check: 'риск-контроль',
  order: 'заявка',
  fill: 'исполнение',
  reconnect: 'реконнект',
  error: 'ошибка',
  stage: 'стадия',
};

/** Human stage label. */
export function botStageLabel(stage) {
  return BOT_STAGE_LABELS[stage] || String(stage || '—');
}

/** Human journal-kind label. */
export function botKindLabel(kind) {
  return BOT_KIND_LABELS[kind] || String(kind || '—');
}

/** Extract the bot identity from one bot_* tool reply. */
export function parseBotFromReply(reply) {
  if (!reply || typeof reply !== 'object') return null;
  const value = reply.result && typeof reply.result === 'object' ? reply.result : reply;
  const bot = value.bot && typeof value.bot === 'object' ? value.bot : value;
  if (!bot || typeof bot.id !== 'string' || bot.id === '') return null;
  return {
    id: bot.id,
    symbol: typeof bot.symbol === 'string' ? bot.symbol : '',
    periodSec: Number.isFinite(bot.periodSec) ? bot.periodSec : 0,
    stage: typeof bot.stage === 'string' ? bot.stage : '',
    qty: Number.isFinite(bot.qty) ? bot.qty : 0,
    fromBacktest: typeof bot.fromBacktest === 'string' ? bot.fromBacktest : '',
  };
}

/** Whether one journal entry carries a decision the agent should comment on. */
export function isSignificantEvent(entry) {
  if (!entry || typeof entry !== 'object') return false;
  switch (entry.kind) {
    case 'signal':
      return entry.signal === 'entry' || entry.signal === 'exit';
    case 'closed_bar':
      return entry.signal === 'entry' || entry.signal === 'exit';
    case 'order':
    case 'fill':
    case 'reconnect':
    case 'error':
    case 'stage':
      return true;
    case 'risk_check':
      return typeof entry.message === 'string' && /отказ|превыш|blocked|reject/i.test(entry.message);
    default:
      return false;
  }
}

/** One-line agent comment for a significant bot event. */
export function eventComment(bot, entry) {
  const symbol = bot && bot.symbol ? bot.symbol : '';
  const message = entry && typeof entry.message === 'string' && entry.message !== '' ? entry.message : '';
  const kind = botKindLabel(entry && entry.kind);
  const prefix = symbol !== '' ? symbol + ': ' : '';
  if (message !== '') return prefix + message + ' (' + kind + ').';
  if (entry && typeof entry.signal === 'string' && entry.signal !== 'none') {
    return prefix + 'сигнал ' + entry.signal + ' на закрытой свече (' + kind + ').';
  }
  return prefix + 'событие бота (' + kind + ').';
}

/** Compact journal line for cards and summaries. */
export function journalLine(entry) {
  if (!entry || typeof entry !== 'object') return '';
  const parts = [botKindLabel(entry.kind)];
  if (typeof entry.close === 'number') parts.push('close ' + entry.close.toFixed(2));
  if (entry.indicators && typeof entry.indicators === 'object') {
    for (const [name, value] of Object.entries(entry.indicators)) {
      if (Number.isFinite(value)) parts.push(name + '=' + value.toFixed(2));
    }
  }
  if (typeof entry.signal === 'string' && entry.signal !== 'none') parts.push('signal ' + entry.signal);
  if (typeof entry.message === 'string' && entry.message !== '') parts.push(entry.message);
  return parts.join(' · ');
}

/** Structured chat-card model of one bot_* tool reply. */
export function botToolCardModel(toolName, args, reply) {
  const payload = reply && typeof reply === 'object' ? reply : {};
  const value = payload.result && typeof payload.result === 'object' ? payload.result : payload;
  if (toolName === 'bot_status' && Array.isArray(value.bots)) {
    return { kind: 'bots', bots: value.bots.map(botCardData), tool: toolName };
  }
  const bot = parseBotFromReply(reply);
  if (bot === null) return null;
  const model = botCardData(bot);
  if (toolName === 'bot_journal' && Array.isArray(value.entries)) {
    model.entries = value.entries.slice(-50);
  }
  if (toolName === 'bot_check' && value.kind !== undefined) {
    model.last = value;
  }
  if (Array.isArray(value.last) && value.last.length > 0) model.last = value.last[value.last.length - 1];
  if (typeof value.proposalId === 'string' && value.proposalId !== '') {
    model.proposalId = value.proposalId;
    model.action = 'promote';
  }
  if (Number.isFinite(args && args.qty)) model.requestedQty = args.qty;
  return model;
}

/** Bot data for one card (no live fetch: the Client polls REST itself). */
export function botCardData(bot) {
  const source = bot && typeof bot === 'object' ? (bot.bot && typeof bot.bot === 'object' ? bot.bot : bot) : {};
  return {
    id: typeof source.id === 'string' ? source.id : '',
    symbol: typeof source.symbol === 'string' ? source.symbol : '',
    periodSec: Number.isFinite(source.periodSec) ? source.periodSec : 0,
    stage: typeof source.stage === 'string' ? source.stage : '',
    qty: Number.isFinite(source.qty) ? source.qty : 0,
    fromBacktest: typeof source.fromBacktest === 'string' ? source.fromBacktest : '',
    last: null,
    entries: [],
    proposalId: '',
    action: '',
    requestedQty: 0,
  };
}

/** Whether the "stop all bots" kill switch must be visible. */
export function killSwitchVisible(bots) {
  if (!Array.isArray(bots) || bots.length === 0) return false;
  return bots.some((bot) => {
    const stage = bot && typeof bot.stage === 'string' ? bot.stage : '';
    return stage !== '' && stage !== 'stopped';
  });
}

/** Text summary of the bot list for the input dock. */
export function botsSummary(bots) {
  if (!Array.isArray(bots) || bots.length === 0) return 'Ботов нет';
  const live = bots.filter((bot) => bot && bot.stage !== 'stopped');
  const parts = bots.map((bot) => {
    const symbol = bot && bot.symbol ? bot.symbol : '—';
    return symbol + ' · ' + botStageLabel(bot && bot.stage);
  });
  return (live.length > 0 ? 'Активных ботов: ' + live.length + ' из ' + bots.length + ' — ' : 'Все боты остановлены (') + parts.join('; ') + (live.length > 0 ? '' : ')');
}

/** Render text of one bot_* tool reply for the model. */
export function botRenderText(toolName, args, reply) {
  const model = botToolCardModel(toolName, args, reply);
  if (model === null) {
    const payload = reply && reply.result !== undefined ? reply.result : reply;
    return JSON.stringify(payload === undefined ? null : payload).slice(0, 1200);
  }
  if (model.kind === 'bots') {
    return model.bots.length === 0
      ? 'Ботов нет.'
      : 'Боты: ' + model.bots.map((bot) => bot.symbol + ' (' + botStageLabel(bot.stage) + ')').join('; ') + '.';
  }
  const parts = [model.symbol + ' · бот ' + model.id + ' · стадия ' + botStageLabel(model.stage) + ' · qty ' + model.qty + '.'];
  if (model.last) parts.push('Последняя запись: ' + journalLine(model.last) + '.');
  if (model.entries && model.entries.length > 0) {
    parts.push('Журнал: ' + model.entries.slice(-5).map(journalLine).join(' | ') + '.');
  }
  if (model.proposalId) parts.push('Требуется подтверждение человека: предложение ' + model.proposalId + ' (бот ещё не переведён на бумагу).');
  return parts.join(' ');
}
