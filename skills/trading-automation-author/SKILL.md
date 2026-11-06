---
name: trading-automation-author
description: Use when the same agent tool workflow runs over and over and should become an automation task — a Go function, an MCP tool, a static or ML model, or a schedule — and when maintaining the terminal automation backlog.
---

# Автор задач автоматизации

Навык объясняет, как превращать повторяющиеся вычисления агента в задачи автоматизации:
дёшево и воспроизводимо вместо повторной оплаты токенов LLM.

## 1. Когда использовать

- В учёте токенов появились повторяющиеся последовательности инструментов (`Ledger.Hotspots`).
- Агент каждый раз заново считает одно и то же: бэктест, walk-forward, подсказки, сверку тикеров.
- Нужно пополнить или пересмотреть `docs/ai-terminal/automation-backlog.md`.
- Пользователь спрашивает «что можно автоматизировать, чтобы не тратить токены?».

## 2. Источник кандидатов

- `terminal/automation`: `Ledger` копит вызовы инструментов и цену LLM-раундов
  (`Record`, `Hotspots(minCalls)`). `Hotspot` — это повторённая последовательность:
  `Sequence []string`, `Calls`, `Tokens`, `Example` (аргументы первого вызова).
- В агенте — готовые точки: `Engine.AutomationHotspots(minCalls)` и инструмент
  `propose_automation`.

## 3. Инструмент `propose_automation`

Аргументы: `minCalls` (≥2, по умолчанию 2), `minTokens` (порог стоимости), `limit`
(до 20), `writeBacklog` (записать в бэклог). Ответ: `hotspots`, `tasks`
(`AutomationTask`) и `written/path`.

`AutomationTask`: `ID` (стабильный `auto-…`), `Title`, `Kind`,
`Rationale`, `Hotspot`, `EstTokensSavedPerWeek`, `Spec` (мини-план), `Status`.

Виды: `go_function`, `mcp_tool`, `static_model`, `ml_model`, `schedule`.
Статусы: `proposed` → `accepted` → `done`.

## 4. Как писать задачу

1. Возьми последовательность и её `Example` за основу контракта.
2. Выбери вид: детерминированный расчёт → `go_function`; чтение данных → `mcp_tool`;
   статистическая формула → `static_model`; обучение офлайн → `ml_model`;
   периодический запуск → `schedule`.
3. Заполни мини-план (`Spec`) в формате плана репозитория:
   `Files:` новые файлы; `Interfaces:` сигнатура/схема; `Тест:` проверка, что результат
   совпадает с ручной последовательностью.
4. Обоснуй (`Rationale`): какие токены экономятся и почему результат детерминирован.

## 5. Бэклог

- Запись — только между маркерами `<!-- auto:begin -->` и `<!-- auto:end -->`.
- Ручной текст вне маркеров не меняется: комментарии, принятые решения, ссылки.
- Повторная запись идемпотентна: задачи с тем же `ID` не дублируются, статус
  `accepted`/`done` сохраняется, а сгенерированные ранее задачи не теряются.
- Принятые задачи ведёт человек: агент только предлагает.

## 6. Ограничения

- Только бумажная торговля и исследования; реальные заявки не автоматизируются.
- Секреты (токены, логины, пароли) — только из окружения, никогда в задаче, Skill или бэклоге.
- Перед автоматизацией вывода проверь выборку и стоимость сделок: `small_sample`/`no_costs`
  (см. `docs/ai-terminal/research.md`).
- Не дублируй существующие инструменты и функции каталога — сначала поищи готовое.

## 7. Пример

Повтор `walk_forward → get_market_hints` три раза за неделю на 2700 токенов →
`go_function`: `Files: terminal/research/…`, `Interfaces: func WalkForwardHints(ctx, in)`,
`Тест: TestWalkForwardHints`.

## 8. Проверка

`go test -race ./terminal/automation ./terminal/agent -run 'Automation|Backlog|ProposeAutomation'`
и `make verify`.
