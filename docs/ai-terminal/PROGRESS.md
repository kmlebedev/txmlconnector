# Прогресс выполнения плана AI-нативного терминала

Источник задач: [docs/superpowers/plans/2026-11-05-ai-native-terminal.md](../superpowers/plans/2026-11-05-ai-native-terminal.md).
Ветка: `plan/ai-terminal-a` (Фаза A), коммиты локальные, без push.

## Правила исполнения (как ведём)

- Каждая задача плана — карточка на доске команды (write-scope, зависимости), TDD: падающий тест -> реализация -> проверки -> commit.
- Проверки перед коммитом: `gofmt`, `go vet ./...`, `go build ./...`, кросс-сборки `native/...`, `go test -race ./...`, плагин — `node --test dsh-ai-terminal/test/`.
- Реверс-инжиниринг не используется (указание пользователя): RE-скиллы не загружались, живые RE-процедуры не запускались.
- Ревью: Go-diff — Lead; фронтенд/JS-diff — вторая модель **Qwen 3.8 Flash** (роут DSH-субагента в этой сессии запрещён, поэтому ревью идёт через Anthropic-совместимый API провайдера; секреты не передаются, только diff и текст задачи). Отчёты — `docs/ai-terminal/reviews/`.
- Живые операции (демо-сервер, ClickHouse, торговые команды) — только с отдельным согласием пользователя; по умолчанию «не проверено (I)».

## Фаза A. Исправления ревью PR #60 — закрыта

| Задача | Карточка | Коммит | Ревью |
|---|---|---|---|
| T1 Guard локального API + токен прокси плагина (R1, R10) | task-1 | `b26b99e` + docs `71d07e5` | Lead OK; JS — Qwen 3.8 Flash (CHANGES -> триаж: P1/P2 отклонены как не соответствующие коду, P3-замечания учтены в документации) |
| T2 Потолки риск-лимитов + строгие int-аргументы (R2, R9) | task-2 | `3051c3e` | Lead OK, P1/P2 нет |
| T3 Без повторных мутаций после сбоя LLM (R3) | task-3 | `75994d0` | Lead OK, P1/P2 нет |
| T4 Автономия через paper-брокер, день МСК (R7) | task-4 | `61c2db0` + `722a07c` | Lead OK; P3: счёт autonomy не сбрасывается между прогонами (задокументировано) |
| T5 Неизвестный период свечей — ошибка, а не 60 с (R8) | task-5 | `6cfd80a` | Lead OK, P1/P2 нет |

Фаза A проверена на чистой выгрузке закоммиченного HEAD (`git archive`): `make verify` — зелёный.

### Что проверено / не проверено

- Проверено: unit/race-тесты задач, `make verify`, node-тесты плагина T1, guard вручную на реальном бинаре (401/403/415/421/cookie), T3/T4 тесты из плана, T5 фикстуры native/records + feed.
- Не проверено (требует живых ресурсов/согласия): плагин внутри живой DSH-сессии (нужен reload DSH + браузер); автономия на живом потоке; ClickHouse/демо-сервер.

## Параллельно начатые задачи следующих фаз

| Задача | Карточка | Коммит | Статус |
|---|---|---|---|
| T11 ClickHouse-клиент + рекордер стакана/сделок (Phase D) | task-6 | `b996425` + `887e253` | Принято Lead; live-запись в ClickHouse не проверена (нет согласия) |
| T12 Новости TRANSAQ с first_seen (Phase D) | task-9 | `3b066d8` | Ядро принято; доделываются SSE-доставка `news` до UI и проводка инструмента `get_news` |
| T23 Декларативные правила из промпта (Phase G) | task-7 | `f5aad81` | Принято Lead, P1/P2 нет |
| T24 Batch-бэктест vs buy & hold (Phase G) | task-8 | — | в работе |
| T14 Replay стакана (Phase D) | task-12 | — | в работе |

## Открытые вопросы Lead

- Живые проверки Step 5 задач 11/12/22/24/26 требуют согласия пользователя и окна демо-сервера/ClickHouse.
- PR фаз: план требует отдельный PR на фазу; push/PR выполняются только по команде пользователя.

## Фаза B — закрыта (ядро агента)

- T6 память диалога — commit `d334f6e`, docs `2342aae`; карточка закрыта. JS-diff ревью Qwen 3.8 Flash (CHANGES): P2/P3 отклонены — `docs/ai-terminal/reviews/2026-11-06-t6-t24-frontend-qwen.md`.
- T7 предложения-подтверждения — commit `23cab2c`, карточка закрыта.
- T8 контекст экрана — commit `582df62`, карточка закрыта.

## Фаза D — закрыта (данные)

| Задача | Коммиты | Статус |
|---|---|---|
| T11 ClickHouse-клиент + рекордер стакана/сделок | `b996425` + `887e253` | код/тесты; live-запись в ClickHouse не проверена (**I**, нет согласия) |
| T12 Новости TRANSAQ с `first_seen` | `3b066d8` + `871a227` | SSE `news` до UI и инструмент `get_news` подключены |
| T13 Винтажный слой (макро/календарь/новости) | `5466c8f` | код/тесты; живые витрины ClickHouse не поднимались (**I**) |
| T14 Replay стакана (queue-aware fills) | `8393410` | код/тесты; живая запись `md.book_events` не проверялась (**I**) |

## Фаза E — закрыта (исследования)

| Задача | Коммиты | Статус |
|---|---|---|
| T15 Walk-forward, deflated Sharpe, PBO, трендовые стратегии | `3241002` + `d583122` | принято, тесты; живых прогонов на TRANSAQ не было (**I**) |
| T16 Оракул волатильности (релизы/новости) | `e8be80d` + `fcae84e` | принято, тесты синтетические; ClickHouse не поднимался (**I**) |
| T17 Отпечатки исполнения/ликвидаций | `a0e7586` | принято; проводки LOB-источника в wiring нет (**I**) |
| T18 Компактные ответы + ledger токенов | `ecfabfd` | принято; `asOf` обязателен, `artifactId`-контракт в research.md §8 |

## Фаза F — закрыта (самообучение)

| Задача | Коммиты | Статус |
|---|---|---|
| T19 SKILL-паттерны (draft/promote по OOS-доказательствам) | `ffa0bf7` + `6248d6b` | принято, 7 Go-тестов + node-провайдер; promotion — только человек/PR |
| T20 Задачи автоматизации из hotpots токенов | `0f16134` | принято; бэклог `docs/ai-terminal/automation-backlog.md` |
| T21 Планировщик автономных research-прогонов с бюджетом токенов | `29e60fa` | принято; живой DSH-прогон не выполнялся (**I**) |

## Фаза G — закрыта (от бэктеста к боту)

- T22 Rule-боты check → dry-run → paper — коммиты `99e6d3f`, `7cc187f`, `9572cc1`; live-стадия отключена, живые закрытые свечи не проверялись (**I**).
- T23 Декларативные правила из промпта — `f5aad81`, принято Lead.
- T24 Batch-бэктест vs buy & hold + графики — `e55fded`, Qwen-ревью chart.js: все P1 отклонены триажем Lead (см. reviews).
- T25 Переносимые навыки `npx skills add` — `adedeac`; ручная установка в чистом Claude Code/Cursor **не выполнялась** (Step 5, статус **I**).
- T26 Пульт бота в DSH-сессии — коммит `ebeaddb` (go-api): карточка/лента журнала бота и кнопки start/stop/promote в чате DSH; контракт — [dsh-integration.md](dsh-integration.md). Живой DSH-прогон не выполнялся (**I**).

## Статус на 2026-11-06 (ветка plan/ai-terminal-a)

Закрыто задач плана: **26 из 26**. T21 — коммит `29e60fa`, T26 (пульт бота в DSH-сессии) —
коммит `ebeaddb`. X1–X3 — внешние репозитории, отдельный scope.

| Фаза | Задачи | Статус |
|---|---|---|
| A. Исправления PR #60 | T1–T5 | **закрыта**, проверена на чистом HEAD (make verify) |
| B. Ядро AI-агента | T6–T8 | **закрыта** (память диалога, proposals+подтверждение, контекст экрана) |
| C. Терминал в DSH | T9–T10 | **закрыта** (инструменты Harness, LLM-мост, провайдер навыков, MCP, панели) |
| D. Данные | T11–T14 | **закрыта** (ClickHouse-клиент, рекордер стакана/сделок/новостей, винтажи, replay) |
| E. Исследования | T15–T18 | **закрыта** (walk-forward/DSR/PBO, оракул волатильности, отпечатки, компактные ответы+ledger) |
| F. Самообучение | T19–T21 | **закрыта** (SKILL-паттерны, automation-бэклог, ночные research-прогоны) |
| G. От бэктеста к боту | T22–T26 | **закрыта** (rule-боты check→dry-run→paper, CRUD/SSE, пульт бота в DSH) |

Ключевые коммиты: T5 `6cfd80a`; T9 `62c5794`; T10 `683cfb7`; T11–T14 (`b996425`, `887e253`, `3b066d8`, `871a227`, `5466c8f`, `8393410`); T15 `3241002` + wiring `d583122`; T16 `e8be80d` + chart `fcae84e`; T17 `a0e7586`; T18 `ecfabfd`; T19 `ffa0bf7` + `6248d6b`; T20 `0f16134`; T21 `29e60fa`; T22 `99e6d3f` + `7cc187f` + `9572cc1`; T23 `f5aad81`; T24 `e55fded`; T25 `adedeac`; T26 `ebeaddb`; T7 `23cab2c`; T8 `582df62`.

### Docs-sync 2026-11-06

Обновлены контракты под фазы C–G: `docs/ai-terminal/ARCHITECTURE.md` (§3 структура, §9.1
инструменты, §10.1 REST/MCP, §10.3 SSE `bot`, §12 wiring, §15 новые пакеты), `docs/ai-terminal/README.md`
(оглавление документов, каталог инструментов, примеры API/MCP/ботов), `data.md` (`get_news`:
обязательный `asOf`, окно `since` относительно `asOf`), `research.md` (T18:
обязательный `asOf`, `artifactId`, актуальные сигнатуры `vol_forecast`/`vol_events`),
`fingerprint.md` (уточнения P3: float только в безразмерных признаках, evidence остаётся в artifact),
`skills/README.md` (automation-author, manual check `npx skills add` — **I**). Коммит —
`docs(ai-terminal): sync the architecture and research contracts with phases D–G`; код не менялся,
кроме комментария в `terminal/lobreplay/book.go`.

### Ревью второй моделью (Qwen 3.8 Flash)

Фронтенд-diff’ы всех фронтенд-задач отправлены Qwen 3.8 Flash (T1, T6, T7, T8, T9-plugin, T16-chart, T24-chart). Отчёты и триаж Lead — `docs/ai-terminal/reviews/`. Роут DSH-субагента `qwen-token-plan` в этой сессии запрещён, поэтому ревью идёт через Anthropic-совместимый API провайдера; модель видит только текст задачи и diff, без секретов и доступа к файлам. Обязательных P1/P2 после триажа не осталось (найденные P1 в основном отклонены как не соответствующие коду; конкретные доводы — в файлах reviews).

### Инцидент с общим git index (устранён)

При параллельных агентах `git add`/`git commit` в общем checkout иногда захватывал staged-файлы других задач. Коммит `649c2ea` (volatility) содержал чужие файлы; Lead расформировал его через `git reset --soft HEAD~1` + unstage, весь код сохранён и пересобран в корректные коммиты. Введён протокол: `git add <ровно свои пути>` -> `git diff --cached --name-only` -> commit; один коммитящий агент за раз (git-токен от Lead); никаких `git add -A`/`.`; коммиты Lead для интеграционных случаев.

### Не проверено (I) / требует живых ресурсов

- Живые проверки плана: демо-сервер TRANSAQ (Step 5 задач 11/12/22), ClickHouse-схемы витрин vintage, живой DSH-прогон плагина/инструментов/панелей (T9/T10/T21/T26), ручной браузерный UI (shift+drag, карточки), скриншоты — не выполнялись: нужен перезапуск Harness/окно демо-сервера/согласие пользователя. Никакие скриншоты не фальсифицировались.
- `npx skills add … --skill trading-terminal` в чистом Claude Code/Cursor (Step 5 T25) **не выполнялся** — статус **I**; проверены только frontmatter/исполняемость скрипта/README тестом `go test ./skills` и smoke `scripts/terminal.sh` против заглушки бэкенда.
- Phase X (винтажи ALFRED, календарь FOMC, схема transaq_quotes) — отдельные репозитории, не трогались.
- Push/PR не выполнялись (по правилам AGENTS.md — только по команде пользователя).
## Финальная приёмка и готовность PR (2026-11-06)

- Все **26 задач плана** закрыты (28 карточек на доске: 26 задач + T9-prep + T-accept). Финальные гейты на HEAD:
  `make verify` = **exit 0**; `node --test dsh-ai-terminal/test/` = **39/39 pass**; `make terminal_smoke` = **37/37 PASS**;
  `make server_build`/`queues_build` = exit 0.
- Шаги плана отмечены в файле плана: **108/116** выполнено; **8** шагов `Live check`/`Manual check` оставлены
  незакрытыми — они требуют живого окружения и согласия пользователя (T9, T10, T11, T21, T22, T24, T25, T26).
- PR-описание и инструкция запуска: [`docs/ai-terminal/PR.md`](PR.md). Ветка `plan/ai-terminal-a` не пушена —
  push/PR выполняются по команде пользователя (AGENTS.md §3/§6).
- Ревью второй моделью: все фронтенд-diff отправлены Qwen 3.8 Flash; обязательных P1/P2 после триажа Lead нет
  (отчёты — `docs/ai-terminal/reviews/`).
- Phase X (внешние репозитории) не входит в этот PR: ALFRED-винтажи, календарь FOMC, схема `transaq_quotes`.
## PR открыт

- PR [#62](https://github.com/kmlebedev/txmlconnector/pull/62) — draft, base `main`, head `plan/ai-terminal-a`; CI «Run Go Tests» — pass.
- Все 26 задач плана закрыты; 8 шагов Live/Manual check остаются до согласия пользователя на живые прогоны.
- Перевод в ready-for-review / merge — после живых Step 5 (по команде пользователя).

## Live/Manual проверки (2026-11-06, ветка plan/ai-terminal-live)

Пользователь дал согласие на живые проверки и указал на соседний проект
`/Users/whitefox/GolandProjects/clickhouse-import-rosstat` (dev-ClickHouse + MCP).

| Шаг | Статус | Доказательство |
|---|---|---|
| T9 Step 5 | **I** | живая DSH-сессия: нужен перезапуск DSH с текущим плагином; в этой сессии не выполнялось |
| T10 Step 5 | **S (частично) / I** | Claude Code 2.1.287: `claude mcp add --transport http terminal http://127.0.0.1:<port>/mcp --header X-Terminal-Token: …` → `claude mcp list` = ✔ Connected; MCP `initialize`/`tools/list` = 32 инструмента, `tools/call` `get_quote` → `result.symbol=TQBR:SBER`; LLM-вызов инструмента из Claude Code не выполнен — OAuth-сессия Claude Code истекла (не блокер кода) |
| T11 Step 5 | **S (схема) + live в окне демо** | `recorder.EnsureSchema` создал `md.book_events/trades/news` на живом ClickHouse, типы колонок совпадают с планом; запись 10 минут и `SELECT kind,count()` запускаются фоновой проверкой по открытию демо-окна (09:51 МСК), результат будет добавлен |
| T12 (news live) | live в окне демо | `md.news` создана; `first_seen/published` проверяются той же фоновой записью |
| T21 Step 5 | **I** | живой DSH/LLM: workflow/schedule/kill-switch проверены node-тестами на fake-сервисах |
| T22 Step 5 | live в окне демо | dry-run бот M5 30 минут на демо-сервере — фоновая проверка |
| T24 Step 5 | **I** | ручной промпт в DSH-чате; API/инструмент `batch_backtest` проверены офлайн-smoke |
| T25 Step 5 | **S (установка) / I (промпт)** | `npx -y skills add <repo> --skill trading-terminal --agent claude-code -y` → установлен `./.claude/skills/trading-terminal` (SKILL.md + scripts/terminal.sh), «Installed 1 skill»; прогон промпта в Claude Code не выполнен из-за истёкшей OAuth-сессии |
| T26 Step 5 | **I** | живой сценарий 7 шагов в DSH — после перезапуска DSH |
| T13/T16 live | **S** | живые dev-витрины `clickhouse-import-rosstat`: см. `data.md` §8 и `research.md` §9; найдено и исправлено смешение категории `cpi` (RU/US) |

Остаются I только шаги, требующие живого DSH-процесса (перезапуск DSH с текущим плагином) — T9/T21/T24/T26;
они не блокируются кодом и оформлены как manual-инструкции в `dsh-integration.md`.
### Дополнительно проверено офлайн (2026-11-06)

- **T24**: `POST /api/agent/tools/batch_backtest/invoke` на офлайн-seed — 3 ряда (GAZP/SBER/LKOH),
  `small_sample` warnings для малых выборок, artifact `batch_backtest` с 3 charts — путь инструмента
  end-to-end (**S**); ручной промпт в DSH-чате остаётся **I**.
- **T24/T9 (tool path)**: `POST /api/agent/tools/backtest_rules/invoke` → `result` с metrics/equity/trades,
  artifact `backtest`; `terminal_get_quote` через MCP — см. выше (**S** для пути инструментов).
- **T21**: тело `dsh-ai-terminal/workflows/nightly-research.js` валидно как JS function body
  (`new Function(...)` parse OK); живой workflow/schedule/kill-switch в DSH — **I** (нужен перезапуск DSH).
- **T26/T9 (клиентская часть)**: карточки/слоты проверены node-тестами на fake-React; живой Harness — **I**.

### Что нужно для живых DSH-шагов (T9/T21/T24/T26)

Профиль DSH `~/.dsh/profiles/desktop` линкует `@local/dsh-ai-terminal` на старый worktree
`txmlconnector/.worktrees/ai-terminal/dsh-ai-terminal` (код PR #60), а не на смёрженный main. Чтобы
проверить живьём, нужно (действие пользователя, внешний конфиг):

1. В `~/.dsh/profiles/desktop/package.json` заменить зависимость на
   `"@local/dsh-ai-terminal": "link:/Users/whitefox/GolandProjects/txmlconnector/dsh-ai-terminal"`.
2. Выполнить `pnpm install` в каталоге профиля (или дать DSH пересобрать зависимости).
3. Перезапустить DSH (текущая сессия завершится).
4. Проверить сценарии: панель «Терминал» → «Автономия» → «Боты», карточка бэктеста в чате (T9/T24),
   кнопку «В чат» / «Спросить о выделенном», ночной workflow (T21), 7-шаговый сценарий бота (T26).
### Инструмент живой приёмки (2026-11-06)

- Добавлен `tools/demo-live-check.sh` + `make terminal_live` (gated `TERMINAL_LIVE_ACCEPTANCE=1`, секреты
  только из `~/re/demo.env`, окно демо-сервера ожидается до 09:55 МСК): T11 — 10 мин записи
  стакана/сделок в `md.*` + `SELECT kind,count()`, T12 — проверка `md.news.first_seen/published`,
  T22 — dry-run бот M5 30 мин с проверкой `closed_bar` по границам МСК и отсутствия заявок.
- `DEMO_CHECK_SELFTEST=1 make terminal_live` — самопроверка без рынка (gates/env/builds/ClickHouse):
  **PASS** (ClickHouse 26.10.1, бинарники собираются).
- Расширены live-тесты `terminal/vintage`: кроме AsOf/Releases/News теперь проверяются путь
  `series_publication` (качество `date`/`fallback`/`exact_time`, строгая граница) и путь latest-only
  (FRED-ряд без публикационной истории) — `TestLiveClickHouseLatestOnlyAndPublicationPaths` PASS.
- Фоновая проверка T11/T12/T22 перезапущена уже этим инструментом; результат появится в
  `.re-run/live/live-summary-*.json` и будет добавлен в этот файл после окна.
### Дополнительные проверки уровня host/backend (2026-11-06, до окна демо)

- **T21 (workflow body, S)**: тело `workflows/nightly-research.js` исполнено в Node с заглушками движка
  (`agent/phase/parallel/pipeline/log`) — 7 фаз, 6 агентских задач, JSON-сводка; промпты вызывают
  `terminal_list_instruments/get_history/vol_forecast/walk_forward/detect_execution_algos/
  list_skill_patterns/propose_automation` и не содержат торговых инструментов. Тест:
  `dsh-ai-terminal/test/nightly-workflow.test.mjs` (2/2 pass). Живой `ctx.workflowEngine` — I.
- **T26 (bot SSE + токен, S)**: на реальном offline-бэкенде открыт SSE `/api/events` с `X-Terminal-Token`,
  создан бот из rules.Spec, выполнен `check` — в потоке получены кадры `type:"bot"` с
  `botId`/`entry` (stage + closed_bar, `signal:"none"`). Маппинг/delivery реле в агента — node-тесты;
  живой клиент DSH (`turnTail`, kill-switch) — I.
- **T9/T24 (tool path, S; UI — I)**: MCP `tools/call get_quote` и REST
  `/api/agent/tools/{backtest_rules,batch_backtest}/invoke` работают на живом бэкенде; карточки
  `tool.call.toolview` — node-тесты; живой DSH-чат — I.
- **T25 (установка навыка, S)**: см. выше; промпт в Claude Code — I (OAuth).
### T11: end-to-end пайплайн recorder → ClickHouse (S, 2026-11-06)

- `native.Client` ↔ `native/testserver` (реальный протокол) → `recorder.Run` → живой ClickHouse:
  `TestLiveRecorderWritesBookAndTradesToClickHouse` PASS — `snapshot`/`level` строки для
  `TQBR:SBER` с `Decimal`-ценами и сделки; живая запись с демо-сервера проверяется фоновой
  приёмкой в окне (09:55 МСК).
- `native/testserver`: добавлен ответ на `RQ_GET_NEWS` пустым списком (recorder опрашивает новости
  внутри Run) — заглушка не рвёт сессию; остальные native-тесты прогоняются.
### T12: news first-seen пайплайн (S локально)

- Тот же live-тест recorder подаёт R_NEWS-заголовки через настоящий протокол: `md.news` получает строку
  с `first_seen > тест-старт`, `published` из заголовка, непустыми `source`/`title`.
  `native/testserver` теперь отвечает на `RQ_GET_NEWS` пустым списком (recorder опрашивает новости).
### Карточки на payload'ах реального бэкенда (T9/T24/T26, S для моделей, 2026-11-06)

- Взяты реальные ответы офлайн-бэкенда (детерминированный seed): `get_quote`, `backtest_rules`
  (artifact `backtest`), `POST /api/bots` + journal (`stage`, `closed_bar`).
- `toolPresentationMeta`/`renderToolResult` дают `kind:"quote"` и `kind:"backtest"` с метриками,
  equity-точками, warnings и корректным текстом с дисклеймером; `parseBotFromReply`/`botCardData`/
  `journalLine`/`isSignificantEvent`/`botRenderText` корректно отрабатывают stage (significant) и
  closed_bar (не значимо).
- Регрессионный тест: `dsh-ai-terminal/test/real-payload-cards.test.mjs` + фикстура
  `test/fixtures/real-cards.json` (обезличенные данные seed, без секретов).
- Живой Harness (фактический рендер React-компонентов и слоты) остаётся **I** до перезапуска DSH.
### T25: промпт через навык на живом агенте (Kimi Code, S-эквивалент, 2026-11-06)

Claude Code OAuth в этой сессии истёк, поэтому промпт-часть Step 5 выполнена на другом живом
агентском CLI с тем же установленным навыком:

- чистый каталог `/tmp/t25-kimi`; `npx -y skills add <repo> --skill trading-terminal --agent kimi-code-cli -y`
  → `./.agents/skills/trading-terminal` (+ `scripts/terminal.sh`);
- запущен офлайн-бэкенд терминала; `kimi -p "Покажи котировку SBER через навык trading-terminal: найди
  SKILL.md, запусти scripts/terminal.sh quote TQBR:SBER..." --skills-dir .agents/skills` → агент нашёл
  SKILL.md, выполнил `scripts/terminal.sh quote TQBR:SBER` и вернул `last=293.22`, bid/ask 293.22/293.26
  (read-only REST, без торговых команд);
- это подтверждает переносимый путь «SKILL.md → scripts/terminal.sh → REST» для любого агента;
  Claude Code-специфичная часть (его OAuth-сессия) остаётся **I**, но установка навыка в `.claude/skills`
  проверена ранее.
### Карточки через настоящий Client-бандл (T9/T24, S для компонентов, 2026-11-06)

- В `dsh-ai-terminal/test/real-payload-cards.test.mjs` добавлены тесты, загружающие реальный
  `client.js` через фейковый `window.__ModuleLoader__` и заглушку React: находятся компоненты
  `tool.call.toolview` для `terminal_run_backtest`/`terminal_get_quote`, рендерятся с `meta`,
  полученной `toolPresentationMeta` из **реальных** payload'ов офлайн-бэкенда.
- Проверено: карточка бэктеста рисует `polyline` equity, текст «Бэктест» и символ; карточка котировки —
  символ и цену `293,22`. Это уровень настоящего React-компонента без браузера/Harness.
- Остаётся **I**: живой Harness-слот/чат и реакция на реальный вызов инструмента моделью — после
  перезапуска DSH.