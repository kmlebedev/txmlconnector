# AI-нативный терминал в DeepSeek Harness: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Терминал из PR #60 становится частью DeepSeek Harness. LLM Harness сам ведёт исследования:
- бэктестит трендовые стратегии на записанном стакане и на винтажных (point-in-time) данных;
- прогнозирует волатильность по новостной ленте и календарю статистики РФ/США, решениям ЦБ и ФРС;
- распознаёт исполняющих роботов;
- оформляет подтверждённые паттерны в SKILL, а повторяющиеся вычисления — в задачи на автоматизацию: Go-функции и MCP-инструменты вместо токенов.

**Architecture:**
- Go-бэкенд `cmd/terminal` остаётся единственным исполнителем вычислений. Это бэктест, оракул волатильности, отпечатки роботов, запись и replay стакана.
- Плагин `dsh-ai-terminal` превращает инструменты бэктенда в нативные инструменты Harness (`ctx.tools.register`). Он также:
  - отдаёт Go-агенту LLM Harness (`ctx.llm.stream`) вместо отдельного API-ключа;
  - регистрирует навыки (`ctx.skills.registerProvider`);
  - запускает автономные циклы через `ctx.workflowEngine` и `ctx.schedule`.
- Данные лежат в ClickHouse. Запись идёт через HTTP-интерфейс на stdlib. Винтажи макроданных берутся из `clickhouse-import-rosstat` (`v_point_in_time_asof`, `events_calendar`, `news_events`).

**Tech Stack:**
- Go 1.27.2, stdlib (ClickHouse — HTTP `FORMAT JSONEachRow`, без драйвера); `native.Client` — источник живых данных.
- Node ESM (Cordis-плагин DSH) с сервисами `tools`, `llm`, `skills`, `workflowEngine`, `schedule`, `sessionController`, `storage`, `webServer`, `subprocess`.
- ClickHouse (база `default` проекта `clickhouse-import-rosstat`, read-only пользователь — для чтения винтажей; отдельная база `md` — для записи рынка).

**Spec:**
- ревью PR #60 (findings R1–R10 ниже);
- постановка пользователя от 2026-11-05 (этот документ, раздел «Постановка»);
- обзор API Harness `docs/ai-terminal/dsh-integration.md` — создаётся в задаче 9 из отчёта по `app.asar/dsh/node_modules/@deepseek-ai/*/README.md`;
- `docs/ai-terminal/ARCHITECTURE.md` (контракт итерации 1);
- публикации Финама — идеи Фазы G:
  - «Бот на RSI за несколько промптов» (smart-lab, 29.10.2026, https://smart-lab.ru/mobile/topic/1368304/) и комментарий к ней;
  - «Собираем стратегию с помощью ИИ и Финам API» (finam.ru, 27.10.2026, https://www.finam.ru/publications/item/sobiraem-strategiyu-s-pomoshchyu-ii-i-finam-api-20261027-1440/).

  Источник данных у нас — прямой native-доступ к TRANSAQ (`native.Client`), Trade API не используется.

## Постановка (кратко)

1. Исправить замечания ревью PR #60 (R1–R10).
2. Плотная интеграция с LLM Harness: терминал — нативные инструменты, панели и навыки Harness, а не iframe с отдельным LLM.
3. Основная задача — бэктестинг трендовых стратегий: walk-forward, без заглядывания вперёд, на записанном стакане.
4. Оракул волатильности по новостной ленте и календарю статистики: Росстат, ЦБ РФ (ключевая ставка), США (CPI, NFP), ФРС (FOMC).
5. Винтажная БД для честного тестирования: значение показателя «как было известно на момент t».
6. Автономный режим через DSH: расписание, workflow, бюджет токенов, kill-switch.
7. По итогам бэктеста на стакане агент сам пишет SKILL-паттерны. Для повторяющихся вычислений он пишет задачи на автоматизацию: статические и ML-модели, MCP-инструменты.
8. Модели отпечатков роботов: исполнение заявок крупных фондов (TWAP/VWAP/айсберг) и брокерские роботы принудительного закрытия по маржин-коллу.
9. Идеи из публикации Финама:
   - навыки ставятся одной командой `npx skills add` в любой агент (Cursor, Claude Code, Qwen Code, Codex), а не только в DSH;
   - стратегия собирается из промпта без написания кода;
   - сравнение нескольких тикеров с buy & hold одной таблицей;
   - график входов и выходов с периодами в позиции и панелью индикатора;
   - токен — только в `.env`, никогда в чат;
   - необработанная (незакрытая) свеча не участвует в сигнале;
   - путь от бэктеста к боту: `--check` → dry-run → бумага (у них → демо → реальный счёт); перед заявкой проверяется позиция, при обрыве потока — переподключение.

   Из комментария к публикации:
   - история не короче 5 лет;
   - комиссии и проскальзывание обязательны.

   Главное требование: переход «бэктест → бот» идёт **в одном окне DSH-сессии**, максимально нативно, и всё, что делает бот (AI-агент), видно прямо в Harness.

## Global Constraints

- Реальная маршрутизация заявок остаётся выключенной. Всё исполнение — бумажный брокер или replay; задача, которая включает live, вне этого плана.
- `native/...` не меняется, кроме задачи 5; новых зависимостей в `go.mod` нет. ClickHouse — через `net/http`, MCP — собственный JSON-RPC на stdlib.
- Секреты только из окружения: `TC_LOGIN`, `TC_PASSWORD`, `CLICKHOUSE_URL`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `MD_CLICKHOUSE_URL`, `TERMINAL_API_TOKEN`. В логах, фикстурах, SKILL-файлах и коммитах их нет. Навыки прямо запрещают агенту просить токен в чате.
- Бэктест по умолчанию считает комиссию (`FeeBps=5`) и проскальзывание (`SlippageBps=1`). Нулевые значения допустимы только явно и дают предупреждение `no_costs` в `Result.Warnings`.
- Сигналы считаются только по закрытым барам: незакрытый последний бар живой истории отбрасывается или помечается `Bar.Partial=true` и стратегиям не передаётся.
- Время — `time.Time` в UTC на границах API. Торговый день и расписания — `Europe/Moscow` через `feed.MoscowLocation()`.
- Каждый исследовательский запрос к данным принимает `asOf time.Time`. Данные, опубликованные позже `asOf`, не используются; это проверяется тестом.
- Тесты не ходят в сеть: ClickHouse и LLM подменяются `httptest.Server`; живые прогоны — под `TERMINAL_IT_CLICKHOUSE=1` с `t.Skip`.
- `make verify` зелёный после каждой задачи; плагин — `node --test dsh-ai-terminal/test/`.
- Документация — на русском, комментарии в коде — на английском. Сообщения коммитов — Conventional Commits.
- Каждая фаза — отдельный PR в `main`. Фаза A обязательна первой.

## Review Focus

1. **Пересмотренное значение статистики.** Бэктест на дату t видит первую публикацию, а не последующую ревизию. Тест `TestVintageAsOfExcludesLaterRevision` — задача 13.
2. **Релиз в середине бара и переход США на летнее время.** Публикация CPI в 8:30 ET попадает в правильный бар МСК и в марте, и в ноябре. Тест `TestEventWindowAcrossUSDST` — задача 15.
3. **Разрыв записи стакана при переподключении.** После reconnect рекордер пишет маркер `gap`, а replay не склеивает книги до и после разрыва. Тест `TestRecorderMarksGapOnReconnect` — задача 11; `TestReplayResetsBookAfterGap` — задача 14.
4. **Переобученный паттерн.** SKILL не переходит в `validated` без out-of-sample окна и минимального числа сделок. Тест `TestSkillPromotionRequiresOOS` — задача 19.
5. **Вызов инструмента из Harness при упавшем бэкенде и при отмене.** Инструмент возвращает понятную ошибку, а `exec.signal` прерывает HTTP-запрос к бэкенду. Тест `tool bridge aborts upstream on signal` — задача 9.
6. **Две сделки за 90 дней (как в примере Финама).** Результат не выдаётся за значимый: ответ содержит предупреждение `small_sample` (меньше 30 сделок или меньше 5 лет на дневках) и не может стать основой SKILL. Тест `TestBatchBacktestFlagsSmallSample` — задача 24.
7. **Незакрытая свеча в живой истории.** RSI на последнем часе не меняется после закрытия часа, сигналы бэктеста и живой ленты совпадают. Тест `TestRulesIgnorePartialBar` — задача 23; `TestBotSignalsMatchBacktest` — задача 22.
8. **Обрыв потока у работающего бота.** После переподключения пропущенные бары догружаются из истории, сигнал не дублируется и не теряется, карточка в Harness продолжает журнал с того же `seq`. Тесты `TestBotReconnectBackfillsWithoutDuplicates` — задача 22; `relay resubscribes after backend restart and resumes from last seq` — задача 26.

---

## Фазы и PR

| Фаза | Задачи | PR / результат |
|---|---|---|
| A. Исправления ревью PR #60 | 1–5 | безопасный API, жёсткие риск-лимиты, без дублей заявок, автономия через брокер, честные даты свечей |
| B. Ядро AI-агента | 6–8 | память диалога, предложения действий с подтверждением, контекст экрана |
| C. Терминал внутри DSH | 9–10 | инструменты, LLM, навыки, карточки и панели Harness; MCP для внешних клиентов |
| D. Данные | 11–14 | запись стакана, сделок и новостей в ClickHouse; винтажный доступ; replay стакана |
| E. Исследования | 15–18 | walk-forward трендовых стратегий, оракул волатильности, отпечатки роботов |
| F. Самообучающийся контур | 19–21 | SKILL-паттерны, задачи на автоматизацию ради экономии токенов, автономный ночной цикл |
| G. От бэктеста к боту в одном окне | 22–26 | стратегия из промпта, сравнение тикеров с графиком, бот check → dry-run → бумага на native-свечах, пульт бота в DSH-сессии, переносимые навыки `npx skills add` |
| X. Внешние репозитории | X1–X3 | ALFRED-винтажи и календарь FOMC, исправление схемы `transaq_quotes` |

Зависимости:
- A → B → C.
- D зависит только от A.
- E зависит от D (задачи 13–14) и B (задача 7).
- F зависит от C и E.
- G: порядок внутри фазы — 23 → 24 → 22 → 26.
  - 23 и 24 зависят только от A.
  - 22 зависит от 4, 7 и 23.
  - 26 — от 9, 10, 22 и 24.
  - 25 — от 10.
  - Задачу 15 удобно делать после 23: трендовые стратегии можно описать правилами.

Параллельно можно вести C, D и G23–G24, затем E15/E16/E17 между собой.

## Карта файлов

| Путь | Ответственность |
|---|---|
| `terminal/api/guard.go` | middleware: Origin/Host/Content-Type, токен |
| `terminal/agent/limits.go` | потолки риск-лимитов и валидация аргументов инструментов |
| `terminal/agent/conversation.go` | память диалога |
| `terminal/agent/proposals.go` | предложения действий и подтверждения |
| `terminal/agent/tools_research.go` | исследовательские инструменты фаз E–F |
| `terminal/chstore/` | клиент ClickHouse HTTP (insert, query), общий для recorder/vintage |
| `terminal/recorder/` | запись стакана, сделок и новостей из `native.Client` |
| `terminal/vintage/` | point-in-time доступ к макроданным, календарю и новостям |
| `terminal/lobreplay/` | event-driven replay стакана и модель исполнения |
| `terminal/research/walkforward.go` | walk-forward, OOS, deflated Sharpe |
| `terminal/backtest/strategy_trend_*.go` | новые трендовые стратегии |
| `terminal/volatility/` | оракул волатильности |
| `terminal/fingerprint/` | признаки и классификаторы исполняющих роботов |
| `terminal/skills/` | генерация, валидация и продвижение SKILL-паттернов |
| `terminal/automation/` | учёт стоимости вычислений и бэклог автоматизации |
| `terminal/mcp/` | MCP streamable-http сервер поверх реестра инструментов |
| `cmd/md-recorder/` | бинарь записи рынка |
| `dsh-ai-terminal/tools.js` | мост инструментов бэкенда в `ctx.tools` |
| `dsh-ai-terminal/llm-bridge.js` | OpenAI-совместимый endpoint поверх `ctx.llm.stream` |
| `dsh-ai-terminal/skills.js` | провайдер навыков |
| `dsh-ai-terminal/autonomy.js` | workflow и расписание автономного цикла |
| `dsh-ai-terminal/workflows/nightly-research.js` | скрипт workflow |
| `skills/trading-*/SKILL.md` | ручные навыки исследования (в репозитории); один источник для DSH и для `npx skills add` |
| `terminal/bot/` | рантайм ботов: стадии check/dry_run/paper, журнал решений, живые закрытые свечи |
| `dsh-ai-terminal/bot-relay.js`, `bot-ui.js` | пульт бота в DSH-сессии: карточки, журнал, комментарии агента, kill-switch |
| `terminal/backtest/rules/` | декларативные стратегии из промпта: индикаторы, условия, фильтры |
| `terminal/research/batch.go` | пакетный бэктест по тикерам с buy & hold и проверкой выборки |
| `docs/ai-terminal/{dsh-integration,data,research}.md` | документация фаз C–E |

---

## Фаза A. Исправления ревью PR #60

### Task 1: Защита локального API и прокси плагина (R1, R10)

**Files:**
- Create: `terminal/api/guard.go`, `terminal/api/guard_test.go`
- Modify: `terminal/api/api.go` (`Handler`, `decodeBody`), `cmd/terminal/main.go` (флаг `-allowed-origins`, env `TERMINAL_API_TOKEN`)
- Modify: `dsh-ai-terminal/index.js` (`proxyToBackend`, `TerminalBackend.startFresh/health`)
- Test: `dsh-ai-terminal/test/proxy.test.mjs`

**Interfaces:**
- Produces:
  - `type GuardConfig struct { AllowedOrigins []string; AllowedHosts []string; Token string }`
  - `func Guard(cfg GuardConfig, next http.Handler) http.Handler`
  - `api.Config.Guard GuardConfig`
- Produces: заголовок `X-Terminal-Token`. Плагин генерирует токен на запуск (`crypto.randomUUID()`), передаёт бэкенду через env `TERMINAL_API_TOKEN` и сам подставляет его в проксируемые запросы.

- [x] **Step 1: Write the failing tests** `TestGuardRejectsForeignOrigin`, `TestGuardRejectsTextPlainPost`, `TestGuardRejectsRebindHost`, `TestGuardRequiresTokenWhenSet`, `TestGuardAllowsSameOriginGET`.
  - Ожидание: POST с `Origin: https://evil.example` → 403 `forbidden_origin`.
  - POST `/api/orders` с `Content-Type: text/plain` → 415.
  - `Host: attacker.example:8090` → 421.
  - При заданном `Token` запрос без заголовка → 401.
  - GET `/api/health` с `Host: 127.0.0.1:8090` → 200.
- [x] **Step 2:** `go test ./terminal/api -run TestGuard -v` → FAIL (`undefined: Guard`).
- [x] **Step 3: Implement `Guard`.** Порядок проверок:
  1. Host — по умолчанию `127.0.0.1:*`, `localhost:*`, `[::1]:*` плюс `AllowedHosts`.
  2. Для небезопасных методов (не GET/HEAD) — Origin, если заголовок есть.
  3. Content-Type `application/json` — только для запросов с телом (`ContentLength != 0`); `DELETE /api/orders/{id}` без тела проходит.
  4. Токен — для всех `/api/*`, кроме `/api/health`. Он принимается из заголовка `X-Terminal-Token` или из cookie `terminal_token` (`HttpOnly; SameSite=Strict; Path=/`). Cookie ставится при отдаче `index.html`, поэтому SPA и SSE работают без правок JS, а межсайтовые запросы cookie не несут.

  `Handler()` оборачивает mux в `Guard`. Дополнительный тест: `TestGuardAcceptsStrictCookieFromIndex`.
- [x] **Step 4: Write the failing node test** `proxy strips credentials and adds token`.
  - Прокси удаляет `cookie`, `authorization`, `x-dsh-*` и добавляет `x-terminal-token`.
  - `health()` принимает бэкенд, только если `/api/health` вернул `instanceId`, равный nonce из env `TERMINAL_INSTANCE_ID`, который плагин передал при запуске.
  - Иначе — фаза `error` «порт занят чужим процессом».
- [x] **Step 5: Implement in `index.js`.** Отказаться от `reused`, если `instanceId` не совпал. Бэкенд добавляет `instanceId` в `/api/health` из env.
- [x] **Step 6:** `make verify && node --test dsh-ai-terminal/test/` → exit 0.
- [x] **Step 7: Commit** `fix(terminal): guard the local API against cross-origin and rebinding requests`

### Task 2: Потолки риск-лимитов и строгие аргументы инструментов (R2, R9)

**Files:**
- Create: `terminal/agent/limits.go`, `terminal/agent/limits_test.go`
- Modify: `terminal/agent/autonomy.go` (`prepareAutonomy`), `terminal/agent/tools.go` (`argInt`, `argInt64`, `invokeTool`)

**Interfaces:**
- Produces:
  - `func clampAutonomy(cfg AutonomyConfig, risk RiskLimits, cash float64) (AutonomyConfig, []string)` — второе значение: предупреждения для лога.
  - `func argIntStrict(args map[string]any, key string, def, min, max int64) (int64, error)`
  - `var errBadArgument = errors.New("agent: bad tool argument")`

- [x] **Step 1: Write the failing tests:**
  - `TestClampAutonomyNeverLoosensRisk`: при `risk.StopLossPct=2` и `cfg.StopLossPct=99` получаем 2; `DailyLossPct=100` → 3; `MaxPositionPct=100` → 25.
  - `TestClampAutonomyBudgetByCash`: budget `1e12` при cash `1e6` → `1e6` и предупреждение.
  - `TestArgIntStrictRejectsFractionAndOverflow`: `2.7`, `1e30`, `NaN`, `"abc"` → `errBadArgument`.
  - `TestPlaceOrderToolRejectsFractionalQty`.
- [x] **Step 2:** `go test ./terminal/agent -run 'Clamp|ArgIntStrict|FractionalQty' -v` → FAIL.
- [x] **Step 3: Implement.** Правило для пользовательских значений: стоп-лосс, дневной убыток, доля позиции = `min(cfg, risk)`; тейк-профит = `max(cfg, 0.1)`.
  - `prepareAutonomy` вызывает `clampAutonomy` и пишет предупреждения в лог автономии как `risk_block`.
  - Целочисленные аргументы (`qty`, `count`, `periodSec`) читаются только через `argIntStrict`. Ошибка становится ошибкой инструмента с текстом для модели.
- [x] **Step 4:** `make verify` → exit 0.
- [x] **Step 5: Commit** `fix(agent): risk limits are ceilings and tool integers are validated`

### Task 3: Никаких повторных побочных эффектов при сбое LLM (R3)

**Files:**
- Modify: `terminal/agent/llm.go` (`Chat`, `chatLLM`), `terminal/agent/tools.go`
- Test: `terminal/agent/llm_test.go`

**Interfaces:**
- Produces:
  - `func isMutatingTool(name string) bool` — `place_order`, `cancel_order`, `start_autonomy`, `stop_autonomy`.
  - `ChatResponse.Partial bool` с JSON-ключом `partial`.
  - `chatLLM` возвращает выполненные вызовы вместе с ошибкой.
  - `PlaceOrder.ClientOrderID string` (`json:"clientOrderId,omitempty"`), `paper.OrderRequest.ClientOrderID`, `var paper.ErrDuplicateClientOrderID`.

- [x] **Step 1: Write the failing test** `TestChatNoFallbackAfterMutatingTool`.
  - Заглушка LLM: первый ответ — `place_order`, второй — HTTP 503.
  - Ожидание: брокер получил ровно один `Place`; ответ с `Partial=true`, `LLM=true`, `ToolCalls` из одного элемента; текст начинается с «LLM недоступен после выполнения действий».
- [x] **Step 2: Write the failing test** `TestChatFallsBackWhenOnlyReadToolsRan`. Только `get_quote`, затем 503 → локальный аналитик, как сейчас.
- [x] **Step 3: Implement.** Если среди выполненных вызовов есть мутирующий, fallback не запускается. Каждая заявка агента получает `ClientOrderID = conversationID + ":" + toolCallID`. Брокер отвергает повтор с тем же ID (`paper.ErrDuplicateClientOrderID`).
- [x] **Step 4:** `go test -race ./terminal/...` → PASS.
- [x] **Step 5: Commit** `fix(agent): never replay mutating tools through the local fallback`

### Task 4: Автономия торгует через бумажного брокера, дневной убыток по торговому дню МСК (R7)

**Files:**
- Modify: `terminal/agent/autonomy.go`, `terminal/agent/types.go` (`Broker` + `PlaceAt`), `cmd/terminal/wiring.go`, `terminal/paper/paper.go`
- Test: `terminal/agent/autonomy_test.go`, `terminal/paper/paper_test.go`

**Interfaces:**
- Consumes: `paper.Broker`.
- Produces:
  - `paper.Broker.PlaceAt(req OrderRequest, fill QuoteView, at time.Time, account string) (Order, error)` — исполнение по цене replay.
  - Счёт автономии `account="autonomy"` с бюджетом, выделенным из кэша основного счёта.
  - `paper.Snapshot.Accounts map[string]AccountView`.

- [x] **Step 1: Write the failing tests:**
  - `TestAutonomyFillsVisibleInPortfolio`: после replay `/api/orders` и `Portfolio().Accounts["autonomy"]` содержат сделки автономии.
  - `TestDailyLossResetsPerMoscowDay`: −2,9 % в каждый из 3 дней при лимите 3 % → не останавливается; −3,1 % внутри одного дня → `stopped_by_risk`.
- [x] **Step 2:** `go test ./terminal/agent ./terminal/paper -run 'Autonomy|DailyLoss' -v` → FAIL.
- [x] **Step 3: Implement.** Дневной убыток считается от equity на начало торгового дня `Europe/Moscow` первого бара этого дня.
- [x] **Step 4:** `make verify` → exit 0.
- [x] **Step 5: Commit** `fix(agent): route autonomy fills through the paper broker per Moscow trading day`

### Task 5: Неизвестный период свечей — ошибка, а не 60 секунд (R8)

**Files:**
- Modify: `native/records/candles.go` (`Candles.Event`), `native/client_history.go` (или файл, где вызывается `Event`), `docs/protocol/history.md`
- Test: `native/records/candles_test.go`

**Interfaces:**
- Produces: `model.Candles.Warnings []string`. Если период не найден в `<candlekinds>` и `Period != 1` (вид 1 = 1 мин, S), события получают предупреждение `unknown candle period`, и время не вычисляется — `Time` нулевой.
- Produces: `feed.History` пробрасывает предупреждение, `/api/.../candles` возвращает 503 `unavailable` «справочник периодов ещё не загружен».

- [x] **Step 1: Write the failing test** `TestCandlesUnknownPeriodDoesNotGuessMinutes`. Каталог без `candlekinds`, период 2 → `Warnings` содержит `unknown candle period 2`; ни у одной свечи год не 1970.
- [x] **Step 2:** `go test ./native/records -run UnknownPeriod -v` → FAIL.
- [x] **Step 3: Implement.** Обновить `history.md` пометкой **I** (поведение клиента, не DLL).
- [x] **Step 4:** `make verify` → exit 0.
- [x] **Step 5: Commit** `fix(native): refuse to date candles of an unknown period`

---

## Фаза B. Ядро AI-агента

### Task 6: Память диалога (R4)

**Files:**
- Create: `terminal/agent/conversation.go`, `terminal/agent/conversation_test.go`
- Modify: `terminal/agent/llm.go` (`chatLLM` берёт историю), `terminal/web/ai.js` (хранит `conversationId`)

**Interfaces:**
- Produces:
  - `type ConversationStore interface { Load(id string) []llmMessage; Append(id string, msgs ...llmMessage); Reset(id string) }`
  - `func newMemoryConversations(maxTurns int, maxBytes int) ConversationStore` — LRU на 64 диалога.
  - `ChatResponse.ConversationID string`
- `ChatRequest.ConversationID` уже есть. Пустой ID → сервер генерирует новый и возвращает его.

- [x] **Step 1: Write the failing tests:**
  - `TestChatRemembersPreviousToolResults`: второй запрос «то же для GAZP» отправляет в LLM историю с прошлым `run_backtest`.
  - `TestConversationTrimsByBytes`: старые сообщения вытесняются, системный промпт остаётся; пары `assistant(tool_calls)`/`tool` не разрываются.
- [x] **Step 2:** `go test ./terminal/agent -run 'Remembers|Trims' -v` → FAIL.
- [x] **Step 3: Implement.** Результаты инструментов в истории — только сжатые (`toolResultForLLM`).
- [x] **Step 4:** `make verify` → exit 0.
- [x] **Step 5: Commit** `feat(agent): keep a bounded conversation history per conversationId`

### Task 7: Предложения действий вместо немедленного исполнения (R5)

**Files:**
- Create: `terminal/agent/proposals.go`, `terminal/agent/proposals_test.go`
- Modify: `terminal/agent/tools.go`, `terminal/api/handlers.go`, `terminal/api/api.go` (маршруты), `terminal/web/ai.js` (карточка «Подтвердить / Отклонить»)

**Interfaces:**
- Produces:
  - `type Proposal struct { ID, Tool string; Args map[string]any; Summary string; CreatedAt, ExpiresAt time.Time; Status string }` — `Status`: `pending|approved|rejected|expired|executed`.
  - `func (e *Engine) Approve(ctx context.Context, id string) (ToolCall, error)`
  - `func (e *Engine) Reject(id string) error`
  - `type ApprovalPolicy struct { Mode string; Envelope RiskLimits; MaxOrdersPerRun int }` — `Mode`: `confirm|envelope`.
- Маршруты:
  - `GET /api/agent/proposals`
  - `POST /api/agent/proposals/{id}/approve`
  - `POST /api/agent/proposals/{id}/reject`
- Produces: SSE-событие `proposal`.

- [x] **Step 1: Write the failing tests:**
  - `TestMutatingToolCreatesProposalInConfirmMode`: `place_order` → брокер не вызван, результат `{proposal:{id,status:"pending"}}`.
  - `TestApproveExecutesOnce`: повторный approve → 409.
  - `TestProposalExpires`: 5 мин по `cfg.Now`.
  - `TestEnvelopeModeAutoApprovesInsideLimits`: в режиме `envelope` (для автономии фазы F) заявка внутри лимитов исполняется сразу, вне лимитов — становится предложением.
- [x] **Step 2:** `go test ./terminal/agent ./terminal/api -run Proposal -v` → FAIL.
- [x] **Step 3: Implement.** Режим по умолчанию — `confirm`; `envelope` включается только явно: конфиг или workflow фазы F.
- [x] **Step 4:** `make verify` → exit 0.
- [x] **Step 5: Commit** `feat(agent): mutating tools produce proposals that a human approves`

### Task 8: Контекст экрана в запросах к агенту

**Files:**
- Modify: `terminal/agent/types.go` (`ChatRequest.Context`), `terminal/agent/llm.go`, `terminal/web/app.js`, `terminal/web/chart.js` (выделение диапазона), `terminal/web/ai.js`
- Test: `terminal/agent/llm_test.go`

**Interfaces:**
- Produces: `type ScreenContext struct { Symbol string; PeriodSec int; From, To time.Time; Selection *Range; ActiveTab string }`, `ChatRequest.Context *ScreenContext`.
- Контекст передаётся отдельным системным сообщением `Текущий экран: …`.

- [x] **Step 1: Write the failing test** `TestChatInjectsScreenContext`: запрос «объясни этот пробой» с `Selection` → в запросе к LLM есть системное сообщение с тикером и интервалом UTC.
- [x] **Step 2:** `go test ./terminal/agent -run ScreenContext -v` → FAIL.
- [x] **Step 3: Implement.** В UI: выделение диапазона на графике (shift+drag) и кнопка «Спросить AI о выделенном».
- [x] **Step 4:** `make verify` → exit 0.
- [x] **Step 5: Commit** `feat(agent): send the visible chart context with chat requests`

---

## Фаза C. Терминал внутри DeepSeek Harness

Факты об API Harness (проверены чтением `app.asar`, 2026-11-05; задача 9 переносит их в `docs/ai-terminal/dsh-integration.md`):
- `ctx.tools.register(defineTool({name, description, parameters, output:{schema, render}, execute(args, exec)}))` — регистрирует инструмент; `exec.signal` нужно уважать.
- `ctx.llm.stream({provider, model, messages, tools?, temperature?, maxTokens?})` — вызов LLM через провайдеры пользователя.
- `ctx.skills.registerProvider(() => ({name, list(), get()}))` — провайдер навыков; каталоги `<project>/.dsh/skills` отслеживаются.
- `ctx.workflowEngine.start({script, meta, args})` — запуск workflow.
- `ctx.schedule.create({title, prompt, timing})` — расписание.
- `ctx.sessionController.createSession/prompt` — сессии.
- `ctx.storageDomain` — хранилище.
- Клиентские слоты: `tool.call.toolview`, `sidebar.right.pane.tab`, `conversation.session.header.actions`, `contextMenu.*`.
- `ctx.uiConversation.inputActions.insertQuote` — вставка в поле ввода.
- Нет динамической регистрации MCP — только строки `dsh.bundle.patch`.

### Task 9: Инструменты терминала — нативные инструменты Harness

**Files:**
- Create: `dsh-ai-terminal/tools.js`, `dsh-ai-terminal/test/tools.test.mjs`, `docs/ai-terminal/dsh-integration.md`
- Create: `terminal/api/tools_invoke.go`, `terminal/api/tools_invoke_test.go`
- Modify: `dsh-ai-terminal/index.js` (`inject` += `tools`), `dsh-ai-terminal/client.js` (`tool.call.toolview` для `terminal_run_backtest`, `terminal_get_quote`, `terminal_proposal`)

**Interfaces:**
- Consumes: `Engine.Tools()`, задача 7 (предложения).
- Produces:
  - `POST /api/agent/tools/{name}/invoke`, тело `{args}` → `{result|error, artifact?, proposal?}`.
  - Это общий путь для DSH и MCP: `func (e *Engine) Invoke(ctx context.Context, name string, args map[string]any) (ToolCall, *Artifact)` — публичная обёртка `executeToolCall`.
- Produces: инструменты Harness `terminal_<name>` для каждого `ToolInfo`. Описание — из Go, `parameters` — JSON-schema из Go. `render` — текстовый итог плюс клиентская карточка.

- [x] **Step 1: Write the failing Go test** `TestInvokeToolEndpoint`: `get_quote` → 200 с `result.symbol`; неизвестный инструмент → 404; мутирующий в режиме `confirm` → `proposal`.
- [x] **Step 2: Write the failing node tests:**
  - `registers one tool per backend tool`
  - `tool bridge aborts upstream on signal` (Review Focus 5)
  - `backend down yields tool error text`
- [x] **Step 3: Implement.** При старте плагин запрашивает `/api/agent/tools` и регистрирует инструменты (`defineTool`, иначе объект той же формы). При рестарте бэкенда — перерегистрирует.

  Проверить на живом Harness, что `@deepseek-ai/dsh-tools` импортируется из локального бандла. Если нет — описать обход в `dsh-integration.md` (статус I→S).
- [x] **Step 4:** `make verify && node --test dsh-ai-terminal/test/` → exit 0.
- [ ] **Step 5: Manual check.** В DSH-сессии модель вызывает `terminal_run_backtest`, и в чате рисуется карточка с equity. Скриншот сохранить в `docs/ai-terminal/screenshot-dsh-tools.png`.
- [x] **Step 6: Commit** `feat(dsh-plugin): expose terminal tools to Harness agents`

### Task 10: Один LLM на всё, навыки, панели и MCP

**Files:**
- Create: `dsh-ai-terminal/llm-bridge.js`, `dsh-ai-terminal/skills.js`, `dsh-ai-terminal/test/llm-bridge.test.mjs`
- Create: `terminal/mcp/server.go`, `terminal/mcp/server_test.go`
- Create: `skills/trading-terminal/SKILL.md` (как пользоваться терминалом из Harness: порядок «бэктест → walk-forward → предложение»)
- Modify: `dsh-ai-terminal/index.js`, `dsh-ai-terminal/client.js`, `dsh-ai-terminal/package.json` (`dsh.bundle.patch` + строка MCP-клиента), `cmd/terminal/main.go`

**Interfaces:**
- Produces:
  - `POST /ai-terminal-ctl/llm/v1/chat/completions` — OpenAI-совместимый ответ без стриминга, внутри `ctx.llm.stream`.
  - Бэкенд, запущенный плагином, получает `LLM_BASE_URL=http://127.0.0.1:<dshPort>/ai-terminal-ctl/llm/v1` и `LLM_API_KEY=<одноразовый токен>`.
  - Провайдер и модель берутся из конфига плагина: `llmProvider`, `llmModel`; по умолчанию `deepseek-official` / `deepseek-flash`.
- Produces: `terminal/mcp`:
  - `func NewServer(invoker ToolInvoker) http.Handler` — MCP streamable-http (`initialize`, `tools/list`, `tools/call`).
  - Монтируется на `/mcp` и защищён `Guard`.
  - `type ToolInvoker interface { Tools() []agent.ToolInfo; Invoke(ctx context.Context, name string, args map[string]any) (agent.ToolCall, *agent.Artifact) }`
- Produces: провайдер навыков `ai-terminal`. Он отдаёт навыки из `<repo>/skills/trading-*` и из каталога сгенерированных паттернов `<dataDir>/skills` (задача 19).
- Produces: клиентские поверхности:
  - вкладка правой панели «Терминал» (`sidebar.right.pane.tab`) со стаканом и позицией;
  - действие в шапке сессии «Отправить график в чат» — `insertQuote` с `ScreenContext` в виде JSON-цитаты;
  - пункт контекстного меню «Спросить о выделенном».

- [x] **Step 1: Write the failing node tests:**
  - `llm bridge maps tool_calls both ways`: OpenAI `tools` → `ctx.llm.stream({tools})` → `finish` с `tool_calls` → OpenAI `choices[0].message.tool_calls`.
  - `llm bridge rejects missing token`.
- [x] **Step 2: Write the failing Go tests** `TestMCPInitializeAndList` и `TestMCPToolCallRoundTrip`: JSON-RPC 2.0, `protocolVersion` из запроса клиента, результат `content:[{type:"text",text:<json>}]`.
- [x] **Step 3: Implement** мост LLM, MCP-сервер, провайдер навыков и клиентские поверхности.
- [x] **Step 4:** `make verify && node --test dsh-ai-terminal/test/` → exit 0.
- [x] **Step 5: Manual check.** Из Claude Code: `claude mcp add --transport http terminal http://127.0.0.1:8090/mcp --header "X-Terminal-Token: $TERMINAL_API_TOKEN"`, затем `tools/list` показывает 14+ инструментов.
- [x] **Step 6: Commit** `feat(dsh-plugin): share the Harness LLM, skills and an MCP endpoint with the terminal`

---

## Фаза D. Данные: стакан, сделки, новости, винтажи

Известные пробелы (обзор 2026-11-05, статус **I** до проверки):
- `transaq_quotes` экспортёра не хранит историю L2 — время не в ключе сортировки, `Int16` объёмы.
- `transaq_trades` — секундная точность.
- Новости TRANSAQ нигде не сохраняются.
- Ревизионные винтажи есть только у `cbr_dip_bulletin`.
- США — только последние значения FRED/BLS.
- FOMC — ручные строки `events_calendar`.

### Task 11: Общий клиент ClickHouse и рекордер стакана и сделок

**Files:**
- Create: `terminal/chstore/{client.go,client_test.go}`
- Create: `terminal/recorder/{recorder.go,schema.go,recorder_test.go}`, `cmd/md-recorder/main.go`
- Create: `docs/ai-terminal/data.md`

**Interfaces:**
- Produces `chstore`:
  - `type Client struct{…}`
  - `func New(baseURL, user, password string, hc *http.Client) *Client`
  - `func (c *Client) Insert(ctx context.Context, table string, rows any) error` — `INSERT … FORMAT JSONEachRow` пачками до 10 000 строк.
  - `func (c *Client) Query(ctx context.Context, sql string, params map[string]string, dst any) error` — `FORMAT JSONEachRow`, параметры через `param_<name>`.
  - `func (c *Client) Exec(ctx context.Context, sql string) error`
- Produces схемы в базе `md` (`schema.go`, `CREATE TABLE IF NOT EXISTS`):
  - `md.book_events(ts DateTime64(3,'UTC'), recv_ts DateTime64(6,'UTC'), session_id UUID, seq UInt64, board LowCardinality(String), sec_code LowCardinality(String), kind Enum8('snapshot'=1,'level'=2,'gap'=3), side Enum8('bid'=1,'ask'=2,'none'=0), price Decimal64(6), qty Int64)`; `ORDER BY (sec_code, ts, seq)`; `PARTITION BY toYYYYMMDD(ts)`.
  - `md.trades(ts DateTime64(3,'UTC'), recv_ts DateTime64(6,'UTC'), board, sec_code, trade_no Int64, price Decimal64(6), qty Int64, side Enum8('buy'=1,'sell'=2,'none'=0), open_interest Int64)`; `ReplacingMergeTree ORDER BY (sec_code, trade_no)`.
- Produces: `func Run(ctx context.Context, cl *native.Client, sink Sink, symbols []string) error`, `type Sink interface { Book([]BookEvent) error; Trades([]TradeEvent) error }`.
  - Цены — из `model.Decimal` без `float64`.
  - Время сделки — `Time` + `Millis`.
  - Снапшот стакана пишется после каждой (пере)подписки, далее — изменения уровней (`-1` → `qty=0`).

- [x] **Step 1: Write the failing tests:**
  - `TestChstoreInsertBatchesJSONEachRow` (`httptest`)
  - `TestChstoreQueryParams`
  - `TestRecorderWritesSnapshotThenLevels`
  - `TestRecorderMarksGapOnReconnect` (Review Focus 3): событие `Reconnected` клиента → строка `kind='gap'`, затем новый `snapshot`.
  - `TestRecorderKeepsMillis`
- [x] **Step 2:** `go test ./terminal/chstore ./terminal/recorder -v` → FAIL.
- [x] **Step 3: Implement.** `cmd/md-recorder` берёт параметры из env `TC_*`, `MD_CLICKHOUSE_URL`, `MD_SYMBOLS`; только read-only подписки.
- [x] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Live check** (с согласия пользователя в чате, окно демо-сервера): 10 минут по `TQBR:SBER`; `SELECT kind, count() FROM md.book_events GROUP BY kind` — есть `snapshot` и `level`. Результат — в `data.md` (**S**).
- [x] **Step 6: Commit** `feat(recorder): record order-book events and trades to ClickHouse`

### Task 12: Новости TRANSAQ с моментом первого появления

**Files:**
- Modify: `terminal/recorder/recorder.go`, `terminal/recorder/schema.go`, `terminal/feed/live.go` (новости в ленту терминала)
- Test: `terminal/recorder/recorder_test.go`

**Interfaces:**
- Produces: `md.news(first_seen DateTime64(3,'UTC'), published DateTime64(3,'UTC'), news_id Int64, source LowCardinality(String), title String, body String, content_hash UInt64)`; `ReplacingMergeTree(first_seen) ORDER BY (news_id)`.
  - `first_seen` — момент получения рекордером: это и есть point-in-time.
- Produces: `feed.Event{Type:"news"}` и инструмент агента `get_news(symbol?, since, limit)`.

- [x] **Step 1: Write the failing tests:**
  - `TestRecorderStoresNewsWithFirstSeen`: тело, пришедшее позже заголовка, не меняет `first_seen`.
  - `TestGetNewsToolFiltersBySince`.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(recorder): store TRANSAQ news with first-seen timestamps`

### Task 13: Винтажный (point-in-time) доступ к макроданным, календарю и новостям

**Files:**
- Create: `terminal/vintage/{vintage.go,calendar.go,news.go,vintage_test.go}`
- Modify: `cmd/terminal/wiring.go` (env `CLICKHOUSE_URL`, read-only пользователь), `docs/ai-terminal/data.md`

**Interfaces:**
- Consumes: `chstore.Client`; представления `clickhouse-import-rosstat`: `v_point_in_time_asof(as_of)`, `series_publication`, `v_events_calendar`, `cbr_publication_calendar`, `v_news_events`, `news_annotation`; задача X1 (`us_alfred_vintage`).
- Produces:
  - `type Store interface { AsOf(ctx context.Context, series string, asOf time.Time) ([]Point, error); Releases(ctx context.Context, from, to time.Time, categories ...string) ([]Release, error); News(ctx context.Context, from, to time.Time, sources ...string) ([]NewsItem, error) }`
  - `type Point struct { Series string; Ref time.Time; Value float64; PublishedAt time.Time; Vintage string }`
  - `type Release struct { At time.Time; Category, Title string; Country string; Exact bool }` — `Category`: `cbr_rate|ru_cpi|ru_gdp|us_cpi|us_nfp|fomc|…`; `Country`: `RU|US`; `Exact=false`, если в источнике только дата.
  - `type NewsItem struct { At time.Time; Source, Title string; Sentiment float64; Tags []string }`
- Правило: `AsOf` возвращает версию, опубликованную строго до `asOf`. Ряды без винтажей отдаются со `Vintage="latest-only"` и предупреждением; бэктест может отказаться от них.

- [x] **Step 1: Write the failing tests:**
  - `TestVintageAsOfExcludesLaterRevision` (Review Focus 1): `httptest` ClickHouse с двумя релизами `unemployment_sa` за один месяц → на дату между релизами — первая версия.
  - `TestVintageLatestOnlyFlagged`.
  - `TestReleasesNormalizedToUTC`: `published_at DateTime('Europe/Moscow')` → UTC.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(vintage): point-in-time macro, release calendar and news access`

### Task 14: Event-driven replay записанного стакана

**Files:**
- Create: `terminal/lobreplay/{book.go,replay.go,fill.go,replay_test.go}`
- Create: `terminal/lobreplay/testdata/sber-10m.jsonl.gz` — обезличенная выгрузка задачи 11; только рыночные данные.

**Interfaces:**
- Consumes: `md.book_events`, `md.trades` (через `chstore` или файл JSONL).
- Produces:
  - `type Event struct { TS time.Time; Kind string; Side string; Price model.Decimal; Qty int64; Trade *Trade }`
  - `type Book struct{…}` с методами `Best() (bid, ask Level)`, `Depth(n int) (bids, asks []Level)`, `Imbalance(n int) float64`.
  - `type Strategy interface { OnEvent(b *Book, ev Event) []Order }`
  - `type FillModel struct { LatencyMs int; QueuePosition bool; FeeBps float64 }`
  - `func Run(ctx context.Context, src Source, s Strategy, fm FillModel) (Result, error)` — `Result` совместим по метрикам с `backtest.Metrics`.
- Исполнение лимитной заявки: позиция в очереди = объём уровня в момент постановки. Заявка исполняется, когда сделки по этой цене съели очередь. Рыночная — проходит по стакану с учётом задержки.

- [x] **Step 1: Write the failing tests:**
  - `TestReplayResetsBookAfterGap` (Review Focus 3)
  - `TestLimitFillRespectsQueue`
  - `TestMarketOrderWalksBook`
  - `TestLatencyDelaysVisibility`
  - `TestReplayDeterministic`: два прогона → одинаковый `Result`.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(lobreplay): event-driven order-book replay with queue-aware fills`

---

## Фаза E. Исследования

### Task 15: Трендовые стратегии и walk-forward

**Files:**
- Create: `terminal/backtest/strategy_trend_tsmom.go`, `strategy_trend_keltner.go`, `strategy_trend_supertrend.go`, `strategy_trend_maribbon.go` (+ тесты)
- Create: `terminal/research/{walkforward.go,stats.go,walkforward_test.go}`, `docs/ai-terminal/research.md`
- Modify: `terminal/backtest/strategies.go` (каталог), `terminal/agent/tools_research.go` (инструмент `walk_forward`), `terminal/agent/tools.go` (`compare_strategies` ранжирует по OOS — R6)

**Interfaces:**
- Produces:
  - `type WalkForwardRequest struct { Bars []backtest.Bar; Strategy string; Grid map[string][]float64; TrainBars, TestBars, StepBars int; FeeBps, SlippageBps float64; Events []vintage.Release; ExcludeEventWindow time.Duration }`
  - `type WalkForwardResult struct { Folds []Fold; OOS backtest.Metrics; IS backtest.Metrics; DeflatedSharpe float64; PBO float64; Trials int }`
  - `func WalkForward(ctx context.Context, req WalkForwardRequest) (WalkForwardResult, error)` — перебор сетки параллельно по `runtime.GOMAXPROCS`.
- `DeflatedSharpe` — по Bailey & López de Prado (число испытаний = размер сетки). `PBO` — CSCV на 16 блоках.
- `Events` с `ExcludeEventWindow` позволяют сравнить результат «с окнами релизов» и «без них».

- [x] **Step 1: Write the failing tests:**
  - `TestWalkForwardNoLookahead`: в стратегию передаётся срез только train+test текущего окна; паника при доступе за пределы.
  - `TestDeflatedSharpePenalizesTrials`
  - `TestCompareStrategiesRanksByOOS`
  - `TestEventWindowAcrossUSDST` (Review Focus 2): релиз 8:30 ET 10.03 и 03.11 → окна 15:30 и 16:30 МСК соответственно.
  - Тесты стратегий на синтетическом тренде и флэте: на тренде есть сделки, на флэте их мало.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(research): trend strategies with walk-forward and deflated Sharpe`

### Task 16: Оракул волатильности по новостям и календарю статистики

**Files:**
- Create: `terminal/volatility/{features.go,model.go,oracle.go,oracle_test.go}`
- Modify: `terminal/agent/tools_research.go` (инструменты `vol_forecast`, `vol_events`), `terminal/web/chart.js` (полосы ожидаемой волатильности и маркеры релизов), `docs/ai-terminal/research.md`

**Interfaces:**
- Consumes: `vintage.Store` (релизы, новости), `backtest.Bar` / `md.trades`.
- Produces:
  - `type Forecast struct { Symbol string; At time.Time; Horizon time.Duration; SigmaPct float64; Multiplier float64; Drivers []Driver; Regime string }` — `Regime`: `calm|normal|event|stress`.
  - `type Driver struct { Kind string; Ref string; Contribution float64 }` — например `release:cbr_rate`, `news_intensity`, `har_daily`.
  - `func Fit(ctx context.Context, bars []backtest.Bar, rel []vintage.Release, news []vintage.NewsItem, asOf time.Time) (*Model, error)`
  - `func (m *Model) Forecast(at time.Time, horizon time.Duration, upcoming []vintage.Release, recentNews []vintage.NewsItem) Forecast`
- Модель (статическая, без внешних библиотек): HAR-RV на реализованной волатильности 5-мин баров (день/неделя/месяц) плюс:
  - дамми окон релизов по категориям;
  - интенсивность новостей — экспоненциально затухающий счётчик, вес по тональности из `news_annotation`.

  Оценка — OLS по `log RV`, обучение только на данных до `asOf`. Метрика качества — QLIKE против наивного HAR. Интерфейс `Model` позволяет заменить реализацию ML-моделью из задачи 20.

- [x] **Step 1: Write the failing tests:**
  - `TestOracleRaisesSigmaBeforeKeyRateDecision`: синтетика, где вокруг решения ЦБ RV ×3 → `Multiplier > 2` в окне решения.
  - `TestOracleFitUsesOnlyDataBeforeAsOf`
  - `TestQLIKEBeatsHARonSynthetic`
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(volatility): event- and news-aware volatility oracle`

### Task 17: Отпечатки исполняющих роботов

**Files:**
- Create: `terminal/fingerprint/{features.go,detectors.go,cluster.go,synthetic.go,fingerprint_test.go}`
- Modify: `terminal/agent/tools_research.go` (инструмент `detect_execution_algos`), `docs/ai-terminal/research.md`

**Interfaces:**
- Consumes: `lobreplay.Event` (сделки и уровни стакана).
- Produces:
  - `type Footprint struct { Kind string; Symbol string; Side string; From, To time.Time; Confidence float64; Evidence map[string]float64 }` — `Kind`: `twap|vwap|iceberg|pov|margin_liquidation|unknown_algo`.
  - `func Detect(ctx context.Context, events []lobreplay.Event, cfg Config) []Footprint`
  - `func Synthesize(kind string, base []lobreplay.Event, params map[string]float64, seed int64) []lobreplay.Event` — внедряет известного робота в реальную ленту для разметки и тестов.
- Признаки по окну и стороне агрессора:
  - мода и энтропия размеров сделок (одинаковые «клипы»);
  - периодичность межсобытийных интервалов — пик автокорреляции и спектра (TWAP);
  - корреляция темпа с внутридневным профилем объёма (VWAP);
  - доля объёма рынка (POV);
  - пополнение уровня тем же объёмом сразу после съедания (айсберг, нужен L2);
  - серия рыночных продаж с ростом агрессии в известные окна брокерских маржин-коллов.

  Окна маржин-коллов — конфиг `MarginCallWindows []string` (`"HH:MM-HH:MM"` МСК); значения по умолчанию — **I**, подтверждаются на данных и документируются. Добавочный признак — совпадение с падением цены за N минут до окна.
- Кластеризация — DBSCAN на стандартизованных признаках (собственная реализация) для `unknown_algo`.
- Ограничение: в анонимной ленте нет `orderno`. Атрибуция вероятностная, `Confidence` обязателен, обвинительных формулировок нет.

- [x] **Step 1: Write the failing tests:**
  - `TestDetectsSyntheticTWAP` (recall ≥ 0,8 на 20 seeds)
  - `TestDetectsSyntheticIceberg`
  - `TestDetectsSyntheticMarginLiquidation`
  - `TestNoFalsePositiveOnShuffledTape`: перемешанная лента → `Confidence < 0,3`.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(fingerprint): detect execution algorithms and margin-call liquidations`

### Task 18: Инструменты исследований для агента с учётом стоимости

**Files:**
- Modify: `terminal/agent/tools_research.go`, `terminal/agent/tools.go` (`toolResultForLLM`), `terminal/agent/llm.go`
- Create: `terminal/automation/{ledger.go,ledger_test.go}`

**Interfaces:**
- Consumes: задачи 13–17.
- Produces инструменты:
  - `walk_forward(symbol, strategy, grid, train, test, asOf)`
  - `lob_backtest(symbol, from, to, strategy, params)`
  - `vol_forecast(symbol, horizon, asOf)`
  - `vol_events(from, to)`
  - `detect_execution_algos(symbol, from, to)`
  - `get_news(...)`

  Все принимают `asOf` и возвращают сжатую сводку (≤ 2 КБ JSON) и `artifactId` полного результата.
- Produces:
  - `type Ledger struct{…}`
  - `func (l *Ledger) Record(conv string, call ToolCall, promptTokens, completionTokens int)`
  - `func (l *Ledger) Hotspots(minCalls int) []Hotspot` — повторяющиеся последовательности инструментов с одинаковой формой аргументов и их суммарные токены.
  - `type Hotspot struct { Sequence []string; Calls int; Tokens int; Example map[string]any }`
- Токены берутся из `usage` ответа LLM (OpenAI-совместимого и моста DSH).

- [x] **Step 1: Write the failing tests:**
  - `TestResearchToolsRequireAsOf`: без `asOf` → ошибка с подсказкой.
  - `TestToolResultCompactUnder2KB`
  - `TestLedgerFindsRepeatedSequence`: 5 диалогов `get_history→run_backtest→compare_strategies` → `Hotspot` c `Calls=5`.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(agent): research tools with compact results and a token ledger`

---

## Фаза F. Самообучающийся контур

### Task 19: Агент пишет SKILL-паттерны по итогам бэктеста

**Files:**
- Create: `terminal/skills/{pattern.go,writer.go,promote.go,skills_test.go}`
- Create: `skills/trading-pattern-author/SKILL.md` — как агент формулирует паттерн, какие доказательства прикладывает и чего не делает.
- Modify: `terminal/agent/tools_research.go` (инструменты `draft_skill_pattern`, `list_skill_patterns`), `dsh-ai-terminal/skills.js`

**Interfaces:**
- Consumes: `research.WalkForwardResult`, `lobreplay.Result`, `volatility.Forecast`, `fingerprint.Footprint`.
- Produces:
  - `type Pattern struct { ID, Title, Hypothesis string; Instruments []string; Conditions []string; Entry, Exit, Risk string; Evidence Evidence; Status string; CreatedAt time.Time }` — `Status`: `draft|validated|retired`.
  - `type Evidence struct { DataFrom, DataTo, AsOf time.Time; Source string; OOS backtest.Metrics; DeflatedSharpe, PBO float64; Trials, OOSTrades int; DataHash string }` — `Source`: `lob|bars`.
  - `func WriteDraft(dir string, p Pattern) (path string, err error)` — пишет `<dir>/<id>/SKILL.md`. Во frontmatter: `name`, `description`, `whenToUse`, `metadata.status`, `metadata.evidence`.
  - `func Promote(p *Pattern, policy PromotionPolicy) error`
  - `type PromotionPolicy struct { MinOOSTrades int; MinDeflatedSharpe float64; MaxPBO float64; MinOOSMonths int }` — значения по умолчанию: 30, 0,95, 0,3, 3.
- Каталог по умолчанию — `<dataDir>/skills` вне git. В репозиторий паттерн попадает только через PR человека.
- Каждый SKILL содержит раздел «Ограничения»: только бумажная торговля, без гарантий доходности. `Status=validated` не включает live — он лишь делает навык видимым для агента.

- [x] **Step 1: Write the failing tests:**
  - `TestSkillPromotionRequiresOOS` (Review Focus 4)
  - `TestWriteDraftFrontmatterParsesInDSHFormat`: frontmatter читается тем же разбором, что и у навыков Harness (`name`/`description` обязательны).
  - `TestSkillHasNoSecretsOrAccounts`: значения из `TC_LOGIN`/`TC_PASSWORD` в тестовом окружении не появляются в файле.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(skills): draft and promote evidence-backed trading pattern skills`

### Task 20: Задачи на автоматизацию вычислений вместо токенов

**Files:**
- Create: `terminal/automation/{backlog.go,backlog_test.go}`
- Create: `skills/trading-automation-author/SKILL.md`
- Modify: `terminal/agent/tools_research.go` (инструмент `propose_automation`)

**Interfaces:**
- Consumes: `Ledger.Hotspots` (задача 18).
- Produces:
  - `type AutomationTask struct { ID, Title, Kind, Rationale string; Hotspot Hotspot; EstTokensSavedPerWeek int; Spec string; Status string }`
    - `Kind`: `go_function|mcp_tool|static_model|ml_model|schedule`.
    - `Status`: `proposed|accepted|done`.
    - `Spec` — мини-план в формате этого документа: Files, Interfaces, тест.
  - `func ProposeFromHotspots(h []Hotspot, minTokens int) []AutomationTask`
  - `func WriteBacklog(path string, tasks []AutomationTask) error` — пишет в `docs/ai-terminal/automation-backlog.md` (между маркерами `<!-- auto:begin -->`/`<!-- auto:end -->`).
- ML-модели — тоже задачи бэклога. Например, градиентный бустинг для `fingerprint` или квантильная регрессия для `volatility`: обучение офлайн (Python в `research/` или Go), инференс через интерфейс `volatility.Model` или `fingerprint.Classifier`. В этой задаче модели не реализуются.

- [x] **Step 1: Write the failing tests:**
  - `TestProposeFromHotspotsRanksByTokens`
  - `TestWriteBacklogIsIdempotent`: повторная запись не дублирует задачи с тем же `ID`.
  - `TestBacklogPreservesManualText`: текст вне маркеров не меняется.
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(automation): turn repeated agent computations into automation tasks`

### Task 21: Автономный ночной цикл через DSH

**Files:**
- Create: `dsh-ai-terminal/autonomy.js`, `dsh-ai-terminal/workflows/nightly-research.js`, `dsh-ai-terminal/test/autonomy.test.mjs`
- Modify: `dsh-ai-terminal/index.js` (`inject` += `workflowEngine`, `schedule`, `sessionController`, `storage`), `dsh-ai-terminal/client.js` (панель «Автономия»: последний прогон, бюджет, kill-switch), `dsh-ai-terminal/README.md`

**Interfaces:**
- Consumes: инструменты `terminal_*` (задачи 9, 18–20).
- Produces: workflow `ai-terminal-nightly` с фазами:
  1. Данные: проверка записи и разрывов за день.
  2. Волатильность: перекалибровка оракула на `asOf=now`.
  3. Тренд: walk-forward по универсуму.
  4. Роботы: отчёт `detect_execution_algos`.
  5. Паттерны: `draft_skill_pattern` и `Promote`.
  6. Автоматизация: `propose_automation`.
  7. Итог: сводка в сессию.
- Produces: расписание `ctx.schedule.create({title:'AI-терминал: ночное исследование', timing:{kind:'cron', expr:'30 0 * * 2-6'}})` — 00:30 МСК после вечерней сессии.
- Produces: конфиг плагина `autonomy: {enabled:false, tokenBudget:200000, maxWallMinutes:60, approvalMode:'confirm'}`.
  - Бюджет токенов контролируется по `usage` из моста LLM (задача 10). При превышении workflow отменяется (`run.cancel()`) с записью причины.

- [x] **Step 1: Write the failing node tests:**
  - `nightly workflow stops on token budget`
  - `schedule is not created when autonomy.enabled is false`
  - `kill switch cancels the running workflow and stop_autonomy`
- [x] **Step 2–4:** red → implement → `node --test dsh-ai-terminal/test/ && make verify`.
- [ ] **Step 5: Manual check.** Ручной запуск workflow из Harness на офлайн-данных. В сессии появляется сводка, в `<dataDir>/skills` — черновики, в `automation-backlog.md` — задачи. Скриншот в `docs/ai-terminal/`.
- [x] **Step 6: Commit** `feat(dsh-plugin): scheduled autonomous research workflow with token budget`

---

## Фаза G. От бэктеста к боту в одном окне Harness

Публикации Финама (smart-lab 29.10.2026; finam.ru 27.10.2026 «Собираем стратегию с помощью ИИ и Финам API») показывают путь в Cursor:
1. Навык.
2. Бэктест промптом.
3. Отдельный скрипт-бот с режимами `--check` → dry-run → `--execute`. Свечи грузятся историей и потоком, расчёт идёт только по закрытым свечам; перед заявкой проверяется позиция; при обрыве потока бот переподключается; останавливается по Ctrl-C в терминале.

Слабые места у них:
- бот — отдельный процесс, и что он делает, видно только в консоли;
- код бота генерируется заново под каждую стратегию;
- API не отличает бумаги бота от купленных вручную.

У нас тот же путь проходит **в одном окне DSH-сессии**:
- бэктест, проверка, dry-run и бумажный бот — одна сущность `Bot`, собранная из тех же правил (задача 23);
- исполняется Go-рантаймом на прямом native-доступе к TRANSAQ: история `gethistorydata` и живые свечи из того же `native.Client`, без Trade API;
- каждое решение бота (закрытая свеча, индикаторы, сигнал, риск-проверка, заявка, переподключение) видно в чате живой карточкой, а агент Harness комментирует значимые события;
- у каждого бота свой бумажный подсчёт (задача 4), поэтому его позиции не смешиваются с ручными.

### Task 22: Рантайм бота: check → dry-run → бумага на живых свечах

**Files:**
- Create: `terminal/bot/{bot.go,stages.go,bars.go,journal.go,manager.go,bot_test.go}`
- Modify: `terminal/feed/live.go` (поток закрытых свечей), `terminal/agent/tools.go` (инструменты `bot_*`), `terminal/api/handlers.go` (REST `/api/bots…`), `terminal/api/sse.go` (событие `bot`), `cmd/terminal/wiring.go`

**Interfaces:**
- Consumes:
  - задача 4: `paper.Broker.PlaceAt`, отдельный счёт `account="bot:<id>"`;
  - задача 7: предложения и подтверждения;
  - задача 23: `rules.Spec`, `rules.Compile`;
  - `feed` (живые свечи из `native.Client`).
- Produces:
  - `type Stage string` — константы `StageCheck="check"`, `StageDryRun="dry_run"`, `StagePaper="paper"`, `StageStopped="stopped"`.
    - `live` в этом плане не существует: `Promote` на него возвращает `ErrLiveDisabled`.
  - `type Bot struct { ID, Symbol string; PeriodSec int; Spec rules.Spec; Qty int64; Stage Stage; FromBacktest string; CreatedAt time.Time }`
  - `type JournalEntry struct { Seq uint64; Time, BarTime time.Time; Kind string; Close float64; Indicators map[string]float64; Signal string; Message string; Data map[string]any }`
    - `Kind`: `closed_bar|signal|risk_check|order|fill|reconnect|error|stage`.
    - `Signal`: `none|entry|exit`.
  - `type Manager struct{…}` с методами:
    - `func NewManager(feed BarStream, broker Broker, approver Approver, now func() time.Time) *Manager` — `Broker` — локальный интерфейс пакета с методами `PlaceAt` и `Position(account, symbol string) int64`.
    - `func (m *Manager) CreateFromBacktest(res backtest.Result, spec rules.Spec, qty int64) (Bot, error)`
    - `func (m *Manager) Check(ctx context.Context, id string) (JournalEntry, error)` — один расчёт по истории, без подписки и без чтения счёта; запись вида `close=… rsi=… signal=…`.
    - `func (m *Manager) Start(ctx context.Context, id string, stage Stage) error`
    - `func (m *Manager) Promote(id string, to Stage) (proposalID string, err error)` — переход `dry_run→paper` только через предложение задачи 7.
    - `type Approver interface { Propose(tool string, args map[string]any, summary string) (id string, err error) }` — реализуется `agent.Engine`; пакет `bot` не импортирует `agent`, чтобы не было цикла.
    - Подтверждённое предложение исполняет `bot_start(id, "paper")`.
    - `func (m *Manager) Stop(id string) error`
    - `func (m *Manager) StopAll() error` — kill-switch.
    - `func (m *Manager) Journal(id string, afterSeq uint64, limit int) []JournalEntry`
    - `func (m *Manager) Subscribe(fn func(BotEvent)) (cancel func())`
  - `type BarStream interface { History(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error); SubscribeClosedBars(ctx context.Context, symbol string, periodSec int) (<-chan backtest.Bar, error) }`
    - Живая реализация в `feed` собирает бары из `<alltrades>` и свечей `native.Client`.
    - Закрытый бар выдаётся по первому тику следующего интервала или по таймеру на границе интервала + 2 с (статус **I**).
    - После reconnect поток выдаёт `reconnect`, догружает пропущенные бары из истории и продолжает без дублей.
  - Инструменты агента:
    - `bot_create_from_backtest(backtestId, qty)`
    - `bot_check(id)`
    - `bot_start(id, stage)` — `stage` ∈ `dry_run|paper`.
    - `bot_promote(id, stage)`
    - `bot_stop(id)`
    - `bot_status(id?)`
    - `bot_journal(id, afterSeq, limit)` — ответ сжат: последние 20 записей и счётчики по `Kind`.
  - REST:
    - `GET /api/bots`
    - `POST /api/bots` (из бэктеста)
    - `POST /api/bots/{id}/check`
    - `POST /api/bots/{id}/start`
    - `POST /api/bots/{id}/promote`
    - `POST /api/bots/{id}/stop`
    - `POST /api/bots/stop-all`
    - `GET /api/bots/{id}/journal?after=&limit=`
  - SSE `bot` с `BotEvent{BotID, Entry}`.
- Правила исполнения:
  - Сигнал считается по закрытым барам.
  - На `paper` перед заявкой проверяется позиция счёта бота: вход только с нуля, продажа не больше позиции.
  - Действуют риск-лимиты задачи 2 и лимит сделок в час.
  - Заявка исполняется по следующей котировке `paper`; каждая заявка получает `ClientOrderID = botID + ":" + barTime`.
  - Журнал бота ограничен 5000 записями в памяти и дублируется в `<dataDir>/bots/<id>.jsonl`.

- [x] **Step 1: Write the failing tests** (фейковый `BarStream`, `cfg.Now` и брокер):
  - `TestBotCheckDoesNotSubscribeOrReadAccount`
  - `TestBotDryRunLogsSignalsWithoutOrders`: на синтетике с RSI ниже 0,2 есть `signal=entry`, а брокер не вызван.
  - `TestBotPaperEntersOnlyFromFlat`: два сигнала входа подряд дают одну заявку.
  - `TestBotPaperSellsNoMoreThanPosition`
  - `TestBotPositionsIsolatedFromManual`: ручная покупка того же тикера не влияет на позицию бота.
  - `TestBotSignalsMatchBacktest`: тот же `Spec` на тех же барах даёт те же сигналы, что `backtest.Run` (связь «бэктест = бот»).
  - `TestBotReconnectBackfillsWithoutDuplicates`
  - `TestBotPromoteToPaperRequiresApproval`
  - `TestBotPromoteToLiveRejected`
  - `TestStopAllStopsEveryBot`
- [x] **Step 2:** `go test ./terminal/bot -v` → FAIL (`undefined: NewManager`).
- [x] **Step 3: Implement** `terminal/bot` и живой `BarStream` в `feed`.
- [x] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Live check** (окно демо-сервера, read-only, с согласия пользователя). Бот `dry_run` по `TQBR:SBER` M5 30 минут: в журнале есть `closed_bar` каждые 5 минут, а время закрытия совпадает с границами МСК. Результат — в `docs/ai-terminal/research.md` (**S**).
- [x] **Step 6: Commit** `feat(bot): rule bots from backtests with check, dry-run and paper stages`

### Task 23: Стратегия из промпта — декларативные правила

**Files:**
- Create: `terminal/backtest/rules/{spec.go,indicators.go,eval.go,rules_test.go}`
- Modify: `terminal/backtest/strategies.go` (стратегия `rules` в каталоге), `terminal/agent/tools_research.go` (инструмент `backtest_rules`), `skills/trading-terminal/SKILL.md` (как переводить промпт в правила)

**Interfaces:**
- Produces:
  - `type Spec struct { Indicators map[string]Indicator; Entry, Exit []Condition; Filters []Condition; Side string; StopLossPct, TakeProfitPct float64 }` — `Side` пока только `long`.
  - `type Indicator struct { Kind string; Period int; Source string; Scale float64 }`
    - `Kind`: `rsi_wilder|sma|ema|atr|donchian_high|donchian_low|roc`.
    - `Source`: `close|high|low|volume`.
    - `Scale=0.01` переводит RSI в шкалу 0–1, как в публикации.
  - `type Condition struct { Left, Op, Right string }`
    - `Op`: `<|<=|>|>=|cross_above|cross_below`.
    - `Left`/`Right` — имя индикатора, `close` или число.
  - `func Compile(spec Spec) (backtest.Strategy, error)` — валидирует имена и периоды; ошибка понятна модели.
  - `backtest.Bar.Partial bool` (`json:"partial,omitempty"`). Живая лента (`feed`) ставит его последнему незакрытому бару. `backtest.Run` и `rules` такие бары не передают стратегии — это реализация Global Constraint о закрытых барах.
- Пример из публикации как фикстура `testdata/rsi_example.json`: `rsi_wilder(14, scale 0.01)`, вход `rsi < 0.2`, выход `rsi > 0.8`, фильтр-расширение `close > sma(200)`.
- Агент присылает JSON-правила вместо кода: это экономит токены и даёт воспроизводимость (`Spec` сохраняется в `Result.Params` как хеш и в истории бэктестов целиком).

- [x] **Step 1: Write the failing tests:**
  - `TestRSIWilderMatchesReference`: эталонные значения RSI(14) Уайлдера на фиксированном ряде, допуск 1e-9.
  - `TestRulesPublicationExampleSignals`: фикстура генерирует входы только при `rsi<0.2` без позиции и выходы при `rsi>0.8`.
  - `TestRulesIgnorePartialBar` (Review Focus 7)
  - `TestCompileRejectsUnknownIndicator`
  - `TestRulesNoLookahead`: значение условия на баре i не зависит от баров > i (мутация будущих баров не меняет сигнал).
- [x] **Step 2–4:** red → implement → `make verify`.
- [x] **Step 5: Commit** `feat(backtest): declarative rule strategies composed from prompts`

### Task 24: Сравнение тикеров с buy & hold, проверка выборки и график сделок

**Files:**
- Create: `terminal/research/{batch.go,batch_test.go}`
- Modify:
  - `terminal/backtest/metrics.go` (предупреждения `small_sample`, `no_costs`);
  - `terminal/agent/tools_research.go` (инструмент `batch_backtest`);
  - `terminal/web/chart.js` (заливка периодов в позиции, панель индикатора с уровнями);
  - `dsh-ai-terminal/client.js` (карточка `terminal_batch_backtest` в `tool.call.toolview`: таблица + мини-график).

**Interfaces:**
- Consumes: `agent.DataSource.History` (native TRANSAQ), задача 23 (`rules.Spec`), `research.WalkForward` (задача 15, если уже есть; иначе только in-sample с предупреждением `in_sample_only`).
- Produces:
  - `type HistoryFunc func(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error)` — в проводке это `DataSource.History` поверх `native.Client`.
  - `type BatchRequest struct { Symbols []string; PeriodSec int; Count int; Strategy string; Params map[string]float64; Rules *rules.Spec; FeeBps, SlippageBps float64 }`
  - `type BatchRow struct { Symbol string; Trades int; ReturnPct, BuyHoldPct, MaxDrawdownPct, Sharpe float64; From, To time.Time; BacktestID string; Warnings []string }`
  - `func Batch(ctx context.Context, hist HistoryFunc, req BatchRequest) ([]BatchRow, error)` — тикеры параллельно, не больше `GOMAXPROCS`. `BacktestID` позволяет одной кнопкой создать бота из строки (задача 22).
- Правила выборки:
  - `small_sample`: `Trades < 30`, или период меньше 5 лет для `periodSec >= 86400`, или меньше 1 года для внутридневных;
  - `no_costs`: `FeeBps == 0 && SlippageBps == 0`.
- Артефакт для графика: `Result.Trades` + ряд индикатора `Series map[string][]float64` для панели.

- [x] **Step 1: Write the failing tests:**
  - `TestBatchBacktestFlagsSmallSample` (Review Focus 6)
  - `TestBatchBuyHoldMatchesFirstLastClose`
  - `TestBatchWarnsNoCosts`
  - `TestBatchContinuesOnOneSymbolError`: ошибка одного тикера — строка с `Warnings:["error: …"]`, остальные посчитаны.
- [x] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Manual check.** Промпт из публикации («RSI 14 на GAZP, LKOH, SBER, сравни с buy & hold») в DSH-чате даёт таблицу, графики с заливкой и предупреждения выборки. Скриншот в `docs/ai-terminal/`.
- [x] **Step 6: Commit** `feat(research): multi-symbol backtests against buy-and-hold with sample checks`

### Task 25: Переносимые навыки — `npx skills add` для любого агента

**Files:**
- Modify: `skills/trading-terminal/SKILL.md`, `skills/trading-pattern-author/SKILL.md`, `skills/trading-automation-author/SKILL.md` (frontmatter совместим и с DSH, и с форматом agent skills)
- Create: `skills/trading-terminal/scripts/terminal.sh`, `skills/README.md`, `skills/skills_test.go`
- Modify: `dsh-ai-terminal/skills.js` (читает тот же каталог `skills/`), `README.md` (раздел установки)

**Interfaces:**
- Consumes: задача 10 (MCP `/mcp`, провайдер навыков).
- Produces: установка в Cursor, Claude Code, Qwen Code и Codex — `npx skills add github.com/kmlebedev/txmlconnector --skill trading-terminal`.
  - Навык описывает два способа работы: MCP-сервер терминала (если запущен) и `scripts/terminal.sh` — curl к REST API с токеном из `.env`.
  - В DSH тот же навык приходит от провайдера плагина: один источник, без копий.
- Правила в тексте навыков:
  - токен терминала (`TERMINAL_API_TOKEN`) и логин/пароль TRANSAQ (`TC_LOGIN`/`TC_PASSWORD`) только в `.env`, никогда в чат;
  - реальные заявки недоступны;
  - перед выводами — проверка выборки и стоимости сделок.

- [x] **Step 1: Write the failing test** `TestSkillsFrontmatterValid`. Каждый `skills/*/SKILL.md`:
  - имеет `name`, равный имени каталога, и `description` короче 1024 символов;
  - не содержит строк, похожих на токены (`[A-Za-z0-9_-]{32,}`);
  - все упомянутые скрипты существуют и исполняемы.
- [x] **Step 2:** `go test ./skills -v` → FAIL.
- [x] **Step 3: Implement** навыки, скрипт и раздел README.
- [x] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Manual check.** `npx skills add <локальный путь репозитория> --skill trading-terminal` в чистом каталоге Claude Code. Затем промпт «покажи котировку SBER из терминала» выполняется через навык.
- [x] **Step 6: Commit** `feat(skills): portable trading skills installable with npx skills add`

### Task 26: Пульт бота в DSH-сессии — бэктест, запуск и наблюдение в одном окне

**Files:**
- Create: `dsh-ai-terminal/bot-relay.js` (host), `dsh-ai-terminal/bot-ui.js` (client), `dsh-ai-terminal/test/bot-relay.test.mjs`
- Modify: `dsh-ai-terminal/index.js` (`inject` += `agents`, `sessionController`), `dsh-ai-terminal/client.js`, `dsh-ai-terminal/tools.js` (карточки `terminal_bot_*`), `skills/trading-terminal/SKILL.md` (сценарий «бэктест → бот»), `dsh-ai-terminal/README.md`

**Interfaces:**
- Consumes:
  - задача 9 (инструменты `terminal_*` и `tool.call.toolview`);
  - задача 10 (общий LLM);
  - задача 22 (REST/SSE ботов и `bot_*`);
  - задача 24 (`BacktestID` в строках сравнения);
  - задача 7 (предложения).
- Produces — клиентские поверхности (слоты из отчёта по API Harness):
  - **Карточка бэктеста** (`tool.call.toolview` для `terminal_run_backtest`, `terminal_backtest_rules`, `terminal_batch_backtest`): график с входами и выходами, заливкой «в позиции» и панелью индикатора с уровнями. Кнопки «Проверить» (`check`), «Запустить dry-run», «Перевести на бумагу». Кнопки вызывают тот же REST, что и инструменты; переход на бумагу открывает предложение с «Подтвердить / Отклонить».
  - **Живая карточка бота** (`conversation.chat.turnTail` у сообщения, где бот создан, и закреплённая мини-версия в `conversation.input.dock`). Показывает:
    - стадию (бейдж `check|dry_run|paper|stopped`);
    - последнюю закрытую свечу (время МСК, close), индикаторы (`rsi=0,19`), сигнал;
    - позицию и PnL счёта бота; мини-график последних 100 баров с отметками сигналов;
    - ленту журнала — последние 50 записей, фильтр по `Kind`.

    Обновляется по SSE `bot` через прокси плагина.
  - **Kill-switch** в `conversation.session.header.actions`: «Стоп всех ботов» → `POST /api/bots/stop-all`, видим, пока есть бот не в `stopped`.
  - **Вкладка правой панели** (`sidebar.right.pane.tab`) «Боты»: список ботов сессии, переход к карточке.
- Produces — host-реле `bot-relay.js`:
  - `function startBotRelay(ctx, backend, opts): () => void`. Подписывается на SSE `bot` и для каждого бота помнит `sessionId`, в котором он создан: инструмент `terminal_bot_*` передаёт `exec.sessionId`.
  - Значимые события (`signal`, `order`, `fill`, `risk_check` с отказом, `reconnect`, `error`, `stage`) доставляются агенту этой сессии через `ctx.agents` (`inject()` в inbox агента). Агент коротко комментирует в чате: «Вход по SBER: RSI 0,19 < 0,2, позиция 0 → заявка 10 шт по бумаге, лимиты в норме».
  - `closed_bar` без сигнала в агента не идёт — только в карточку. Так токены тратятся на решения, а не на каждую свечу.
  - Лимит комментариев: `relay.maxAgentEventsPerHour` (по умолчанию 12); лишние события копятся и уходят одной сводкой. Расход записывается в бюджет токенов задачи 21.
- Сценарий в одном окне (проверяется вручную, Step 5):
  1. Промпт «RSI 14 0,2/0,8 на SBER, GAZP, LKOH, сравни с buy & hold».
  2. Таблица и графики.
  3. Кнопка «Проверить» у строки SBER → `close=… rsi=… signal=none`.
  4. «Запустить dry-run» → живая карточка, журнал свечей.
  5. Сигнал → комментарий агента.
  6. «Перевести на бумагу» → подтверждение → заявка видна в карточке и в портфеле терминала.
  7. «Стоп всех ботов».

- [x] **Step 1: Write the failing node tests:**
  - `relay delivers signal events to the bot session agent`
  - `relay keeps closed_bar out of the agent`
  - `relay batches events above the hourly limit into one summary`
  - `relay resubscribes after backend restart and resumes from last seq` (по `afterSeq` журнала, без дублей)
  - `kill switch action calls stop-all and hides when no bot runs`
- [x] **Step 2:** `node --test dsh-ai-terminal/test/bot-relay.test.mjs` → FAIL.
- [x] **Step 3: Implement** реле, карточки и действия. Если `ctx.agents.inject` недоступен из плагина (проверить в задаче 9), запасной путь — `ctx.sessionController.prompt` с системной пометкой `[bot:<id>]`; выбор записать в `docs/ai-terminal/dsh-integration.md`.
- [x] **Step 4:** `node --test dsh-ai-terminal/test/ && make verify` → exit 0.
- [ ] **Step 5: Manual check** на офлайн-снапшоте и затем на живом read-only потоке: сценарий из 7 шагов выше в одной DSH-сессии. Скриншоты — `docs/ai-terminal/screenshot-dsh-bot-{backtest,live,signal}.png`.
- [x] **Step 6: Commit** `feat(dsh-plugin): bot control room inside the Harness session`

---

## Фаза X. Внешние репозитории (отдельные PR в своих репозиториях)

- **X1. `clickhouse-import-rosstat`: винтажи США из ALFRED.** Таблица `us_alfred_vintage(series, date, realtime_start Date, realtime_end Date, value Float64)` для `CPIAUCSL, PAYEMS, UNRATE, FEDFUNDS, GDP`. Представление `v_us_point_in_time_asof(as_of)` по образцу `v_point_in_time_asof`. Нужен ключ `FRED_API_KEY`, только из env.
- **X2. `clickhouse-import-rosstat`: календарь FOMC и релизов США.** Автоматическая загрузка дат FOMC (федеральный календарь) и BLS release schedule в `events_calendar` с `event_time` в ET и конвертацией в МСК. Это заменяет ручные строки `sql/events_calendar_*.sql`. Также `series_publication` для ключевых рядов Росстата: ИПЦ, ВВП.
- **X3. `transaq-clickhouse-exporter`: схема `transaq_quotes`.** Время в ключе сортировки и `Int64` объёмы — или отказ от таблицы в пользу `md.book_events` из задачи 11. Миллисекунды в `transaq_trades`.

Задачи 13 и 16 работают и без X1/X2: ряды без винтажей помечаются `latest-only`, а релизы без точного времени — `Exact=false`. Тест `TestVintageLatestOnlyFlagged` это закрепляет.

---

## Замечания ревью PR #60 → задачи

| ID | Замечание | Задача |
|---|---|---|
| R1 | нет защиты от CSRF/DNS-rebinding у локального API | 1 |
| R2 | LLM ослабляет риск-лимиты автономии | 2 |
| R3 | fallback повторяет мутирующие инструменты | 3 |
| R4 | нет памяти диалога | 6 |
| R5 | нет подтверждения действий человеком | 7 |
| R6 | выбор стратегии in-sample | 15 |
| R7 | «дневной» убыток за весь прогон, автономия мимо брокера | 4 |
| R8 | тихий fallback периода свечей 60 с | 5 |
| R9 | непроверенное приведение float→int аргументов | 2 |
| R10 | плагин доверяет любому процессу на порту | 1 |
