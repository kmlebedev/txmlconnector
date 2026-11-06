// Nightly research workflow body (plan Task 21, workflow "ai-terminal-nightly").
//
// This file is NOT imported: the plugin reads it as text and passes it to
// ctx.workflowEngine.start({ script }), where it runs as a plain JavaScript
// body with top-level await and one JSON return value. The only names in scope
// are the engine hooks: agent(), parallel(), pipeline(), phase() and log().
// Child agents call the plugin's terminal_* tools; they must not execute
// mutating actions — proposals are for the human to approve.

phase('Данные', 'проверка записи и разрывов за день');
const data = await agent(
  'Проверь качество данных AI-терминала за прошедший торговый день. ' +
  'Вызови инструменты terminal_list_instruments и terminal_get_history для SBER ' +
  '(periodSec 86400, count 30). Найди разрывы в дневных барах и последнюю дату записи. ' +
  'Верни короткий текст: сколько инструментов, есть ли разрывы, последний бар.',
  { label: 'data-audit' },
);

phase('Волатильность', 'перекалибровка оракула на asOf=now');
const volatility = await agent(
  'Оцени волатильность SBER, GAZP и LKOH на текущий момент. Вызови terminal_vol_forecast ' +
  'для каждого тикера (periodSec 86400) и terminal_vol_events, если нужен контекст событий. ' +
  'Верни текст: прогноз волатильности по тикерам и есть ли аномалии относительно истории.',
  { label: 'volatility-recalibration' },
);

phase('Тренд', 'walk-forward по универсуму');
const trend = await agent(
  'Проверь устойчивость трендовых стратегий walk-forward по SBER, GAZP, LKOH. ' +
  'Вызови terminal_walk_forward для стратегий sma_cross и donchian_breakout, periodSec 86400. ' +
  'Верни текст: по каким тикерам out-of-sample результат положителен и где выборка мала. ' +
  'Не подгоняй параметры под тестовый отрезок.',
  { label: 'walk-forward' },
);

phase('Роботы', 'отчёт detect_execution_algos');
const algos = await agent(
  'Проверь исполнение на бирже. Вызови terminal_detect_execution_algos по SBER и GAZP ' +
  '(periodSec 60) и terminal_lob_backtest, если нужен стакан. ' +
  'Верни текст: замечены ли алгоритмические паттерны исполнения и на каких тикерах.',
  { label: 'execution-algos' },
);

phase('Паттерны', 'черновики навыков из найденных закономерностей');
const patterns = await agent(
  'Собери черновики навыков из ночных находок. Сначала вызови terminal_list_skill_patterns, ' +
  'затем terminal_draft_skill_pattern для одной-двух закономерностей с чётким доказательством ' +
  '(например, устойчивый walk-forward по тикеру). Не создавай навык из одного наблюдения. ' +
  'Верни текст: какие черновики созданы и почему.',
  { label: 'skill-patterns' },
);

phase('Автоматизация', 'повторяющиеся вычисления -> задачи');
const automation = await agent(
  'Предложи автоматизации для вычислений, которые повторяются каждую ночь. ' +
  'Вызови terminal_propose_automation с найденными повторяющимися отчётами. ' +
  'Верни текст: какие задачи предложены и в чём их польза для пользователя.',
  { label: 'automation-proposals' },
);

phase('Итог', 'сводка в сессию');
return {
  workflow: 'ai-terminal-nightly',
  generatedAt: new Date().toISOString(),
  data,
  volatility,
  trend,
  algos,
  patterns,
  automation,
  summary:
    'Ночное исследование AI-терминала завершено: данные проверены, волатильность ' +
    'перекалибрована, walk-forward и исполнение оценены, черновики навыков и ' +
    'предложения автоматизации подготовлены. Мутирующие действия не выполнялись — ' +
    'предложения ждут подтверждения человека.',
};
