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
- `docs/ai-terminal/ARCHITECTURE.md` (контракт итерации 1).

## Постановка (кратко)

1. Исправить замечания ревью PR #60 (R1–R10).
2. Плотная интеграция с LLM Harness: терминал — нативные инструменты, панели и навыки Harness, а не iframe с отдельным LLM.
3. Основная задача — бэктестинг трендовых стратегий: walk-forward, без заглядывания вперёд, на записанном стакане.
4. Оракул волатильности по новостной ленте и календарю статистики: Росстат, ЦБ РФ (ключевая ставка), США (CPI, NFP), ФРС (FOMC).
5. Винтажная БД для честного тестирования: значение показателя «как было известно на момент t».
6. Автономный режим через DSH: расписание, workflow, бюджет токенов, kill-switch.
7. По итогам бэктеста на стакане агент сам пишет SKILL-паттерны. Для повторяющихся вычислений он пишет задачи на автоматизацию: статические и ML-модели, MCP-инструменты.
8. Модели отпечатков роботов: исполнение заявок крупных фондов (TWAP/VWAP/айсберг) и брокерские роботы принудительного закрытия по маржин-коллу.

## Global Constraints

- Реальная маршрутизация заявок остаётся выключенной. Всё исполнение — бумажный брокер или replay; задача, которая включает live, вне этого плана.
- `native/...` не меняется, кроме задачи 5; новых зависимостей в `go.mod` нет. ClickHouse — через `net/http`, MCP — собственный JSON-RPC на stdlib.
- Секреты только из окружения: `TC_LOGIN`, `TC_PASSWORD`, `CLICKHOUSE_URL`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `MD_CLICKHOUSE_URL`, `TERMINAL_API_TOKEN`. В логах, фикстурах, SKILL-файлах и коммитах их нет.
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
| X. Внешние репозитории | X1–X3 | ALFRED-винтажи и календарь FOMC, исправление схемы `transaq_quotes` |

Зависимости: A → B → C. D зависит только от A. E зависит от D (задачи 13–14) и B (задача 7). F зависит от C и E.
Параллельно можно вести C и D, затем E15/E16/E17 между собой.

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
| `.dsh/skills/trading-*/SKILL.md` | ручные навыки исследования (в репозитории) |
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

- [ ] **Step 1: Write the failing tests** `TestGuardRejectsForeignOrigin`, `TestGuardRejectsTextPlainPost`, `TestGuardRejectsRebindHost`, `TestGuardRequiresTokenWhenSet`, `TestGuardAllowsSameOriginGET`.
  - Ожидание: POST с `Origin: https://evil.example` → 403 `forbidden_origin`.
  - POST `/api/orders` с `Content-Type: text/plain` → 415.
  - `Host: attacker.example:8090` → 421.
  - При заданном `Token` запрос без заголовка → 401.
  - GET `/api/health` с `Host: 127.0.0.1:8090` → 200.
- [ ] **Step 2:** `go test ./terminal/api -run TestGuard -v` → FAIL (`undefined: Guard`).
- [ ] **Step 3: Implement `Guard`.** Порядок проверок:
  1. Host — по умолчанию `127.0.0.1:*`, `localhost:*`, `[::1]:*` плюс `AllowedHosts`.
  2. Для небезопасных методов (не GET/HEAD) — Origin, если заголовок есть.
  3. Content-Type `application/json` — только для запросов с телом (`ContentLength != 0`); `DELETE /api/orders/{id}` без тела проходит.
  4. Токен — для всех `/api/*`, кроме `/api/health`. Он принимается из заголовка `X-Terminal-Token` или из cookie `terminal_token` (`HttpOnly; SameSite=Strict; Path=/`). Cookie ставится при отдаче `index.html`, поэтому SPA и SSE работают без правок JS, а межсайтовые запросы cookie не несут.

  `Handler()` оборачивает mux в `Guard`. Дополнительный тест: `TestGuardAcceptsStrictCookieFromIndex`.
- [ ] **Step 4: Write the failing node test** `proxy strips credentials and adds token`.
  - Прокси удаляет `cookie`, `authorization`, `x-dsh-*` и добавляет `x-terminal-token`.
  - `health()` принимает бэкенд, только если `/api/health` вернул `instanceId`, равный nonce из env `TERMINAL_INSTANCE_ID`, который плагин передал при запуске.
  - Иначе — фаза `error` «порт занят чужим процессом».
- [ ] **Step 5: Implement in `index.js`.** Отказаться от `reused`, если `instanceId` не совпал. Бэкенд добавляет `instanceId` в `/api/health` из env.
- [ ] **Step 6:** `make verify && node --test dsh-ai-terminal/test/` → exit 0.
- [ ] **Step 7: Commit** `fix(terminal): guard the local API against cross-origin and rebinding requests`

### Task 2: Потолки риск-лимитов и строгие аргументы инструментов (R2, R9)

**Files:**
- Create: `terminal/agent/limits.go`, `terminal/agent/limits_test.go`
- Modify: `terminal/agent/autonomy.go` (`prepareAutonomy`), `terminal/agent/tools.go` (`argInt`, `argInt64`, `invokeTool`)

**Interfaces:**
- Produces:
  - `func clampAutonomy(cfg AutonomyConfig, risk RiskLimits, cash float64) (AutonomyConfig, []string)` — второе значение: предупреждения для лога.
  - `func argIntStrict(args map[string]any, key string, def, min, max int64) (int64, error)`
  - `var errBadArgument = errors.New("agent: bad tool argument")`

- [ ] **Step 1: Write the failing tests:**
  - `TestClampAutonomyNeverLoosensRisk`: при `risk.StopLossPct=2` и `cfg.StopLossPct=99` получаем 2; `DailyLossPct=100` → 3; `MaxPositionPct=100` → 25.
  - `TestClampAutonomyBudgetByCash`: budget `1e12` при cash `1e6` → `1e6` и предупреждение.
  - `TestArgIntStrictRejectsFractionAndOverflow`: `2.7`, `1e30`, `NaN`, `"abc"` → `errBadArgument`.
  - `TestPlaceOrderToolRejectsFractionalQty`.
- [ ] **Step 2:** `go test ./terminal/agent -run 'Clamp|ArgIntStrict|FractionalQty' -v` → FAIL.
- [ ] **Step 3: Implement.** Правило для пользовательских значений: стоп-лосс, дневной убыток, доля позиции = `min(cfg, risk)`; тейк-профит = `max(cfg, 0.1)`.
  - `prepareAutonomy` вызывает `clampAutonomy` и пишет предупреждения в лог автономии как `risk_block`.
  - Целочисленные аргументы (`qty`, `count`, `periodSec`) читаются только через `argIntStrict`. Ошибка становится ошибкой инструмента с текстом для модели.
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Commit** `fix(agent): risk limits are ceilings and tool integers are validated`

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

- [ ] **Step 1: Write the failing test** `TestChatNoFallbackAfterMutatingTool`.
  - Заглушка LLM: первый ответ — `place_order`, второй — HTTP 503.
  - Ожидание: брокер получил ровно один `Place`; ответ с `Partial=true`, `LLM=true`, `ToolCalls` из одного элемента; текст начинается с «LLM недоступен после выполнения действий».
- [ ] **Step 2: Write the failing test** `TestChatFallsBackWhenOnlyReadToolsRan`. Только `get_quote`, затем 503 → локальный аналитик, как сейчас.
- [ ] **Step 3: Implement.** Если среди выполненных вызовов есть мутирующий, fallback не запускается. Каждая заявка агента получает `ClientOrderID = conversationID + ":" + toolCallID`. Брокер отвергает повтор с тем же ID (`paper.ErrDuplicateClientOrderID`).
- [ ] **Step 4:** `go test -race ./terminal/...` → PASS.
- [ ] **Step 5: Commit** `fix(agent): never replay mutating tools through the local fallback`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestAutonomyFillsVisibleInPortfolio`: после replay `/api/orders` и `Portfolio().Accounts["autonomy"]` содержат сделки автономии.
  - `TestDailyLossResetsPerMoscowDay`: −2,9 % в каждый из 3 дней при лимите 3 % → не останавливается; −3,1 % внутри одного дня → `stopped_by_risk`.
- [ ] **Step 2:** `go test ./terminal/agent ./terminal/paper -run 'Autonomy|DailyLoss' -v` → FAIL.
- [ ] **Step 3: Implement.** Дневной убыток считается от equity на начало торгового дня `Europe/Moscow` первого бара этого дня.
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Commit** `fix(agent): route autonomy fills through the paper broker per Moscow trading day`

### Task 5: Неизвестный период свечей — ошибка, а не 60 секунд (R8)

**Files:**
- Modify: `native/records/candles.go` (`Candles.Event`), `native/client_history.go` (или файл, где вызывается `Event`), `docs/protocol/history.md`
- Test: `native/records/candles_test.go`

**Interfaces:**
- Produces: `model.Candles.Warnings []string`. Если период не найден в `<candlekinds>` и `Period != 1` (вид 1 = 1 мин, S), события получают предупреждение `unknown candle period`, и время не вычисляется — `Time` нулевой.
- Produces: `feed.History` пробрасывает предупреждение, `/api/.../candles` возвращает 503 `unavailable` «справочник периодов ещё не загружен».

- [ ] **Step 1: Write the failing test** `TestCandlesUnknownPeriodDoesNotGuessMinutes`. Каталог без `candlekinds`, период 2 → `Warnings` содержит `unknown candle period 2`; ни у одной свечи год не 1970.
- [ ] **Step 2:** `go test ./native/records -run UnknownPeriod -v` → FAIL.
- [ ] **Step 3: Implement.** Обновить `history.md` пометкой **I** (поведение клиента, не DLL).
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Commit** `fix(native): refuse to date candles of an unknown period`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestChatRemembersPreviousToolResults`: второй запрос «то же для GAZP» отправляет в LLM историю с прошлым `run_backtest`.
  - `TestConversationTrimsByBytes`: старые сообщения вытесняются, системный промпт остаётся; пары `assistant(tool_calls)`/`tool` не разрываются.
- [ ] **Step 2:** `go test ./terminal/agent -run 'Remembers|Trims' -v` → FAIL.
- [ ] **Step 3: Implement.** Результаты инструментов в истории — только сжатые (`toolResultForLLM`).
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Commit** `feat(agent): keep a bounded conversation history per conversationId`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestMutatingToolCreatesProposalInConfirmMode`: `place_order` → брокер не вызван, результат `{proposal:{id,status:"pending"}}`.
  - `TestApproveExecutesOnce`: повторный approve → 409.
  - `TestProposalExpires`: 5 мин по `cfg.Now`.
  - `TestEnvelopeModeAutoApprovesInsideLimits`: в режиме `envelope` (для автономии фазы F) заявка внутри лимитов исполняется сразу, вне лимитов — становится предложением.
- [ ] **Step 2:** `go test ./terminal/agent ./terminal/api -run Proposal -v` → FAIL.
- [ ] **Step 3: Implement.** Режим по умолчанию — `confirm`; `envelope` включается только явно: конфиг или workflow фазы F.
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Commit** `feat(agent): mutating tools produce proposals that a human approves`

### Task 8: Контекст экрана в запросах к агенту

**Files:**
- Modify: `terminal/agent/types.go` (`ChatRequest.Context`), `terminal/agent/llm.go`, `terminal/web/app.js`, `terminal/web/chart.js` (выделение диапазона), `terminal/web/ai.js`
- Test: `terminal/agent/llm_test.go`

**Interfaces:**
- Produces: `type ScreenContext struct { Symbol string; PeriodSec int; From, To time.Time; Selection *Range; ActiveTab string }`, `ChatRequest.Context *ScreenContext`.
- Контекст передаётся отдельным системным сообщением `Текущий экран: …`.

- [ ] **Step 1: Write the failing test** `TestChatInjectsScreenContext`: запрос «объясни этот пробой» с `Selection` → в запросе к LLM есть системное сообщение с тикером и интервалом UTC.
- [ ] **Step 2:** `go test ./terminal/agent -run ScreenContext -v` → FAIL.
- [ ] **Step 3: Implement.** В UI: выделение диапазона на графике (shift+drag) и кнопка «Спросить AI о выделенном».
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Commit** `feat(agent): send the visible chart context with chat requests`

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

- [ ] **Step 1: Write the failing Go test** `TestInvokeToolEndpoint`: `get_quote` → 200 с `result.symbol`; неизвестный инструмент → 404; мутирующий в режиме `confirm` → `proposal`.
- [ ] **Step 2: Write the failing node tests:**
  - `registers one tool per backend tool`
  - `tool bridge aborts upstream on signal` (Review Focus 5)
  - `backend down yields tool error text`
- [ ] **Step 3: Implement.** При старте плагин запрашивает `/api/agent/tools` и регистрирует инструменты (`defineTool`, иначе объект той же формы). При рестарте бэкенда — перерегистрирует.

  Проверить на живом Harness, что `@deepseek-ai/dsh-tools` импортируется из локального бандла. Если нет — описать обход в `dsh-integration.md` (статус I→S).
- [ ] **Step 4:** `make verify && node --test dsh-ai-terminal/test/` → exit 0.
- [ ] **Step 5: Manual check.** В DSH-сессии модель вызывает `terminal_run_backtest`, и в чате рисуется карточка с equity. Скриншот сохранить в `docs/ai-terminal/screenshot-dsh-tools.png`.
- [ ] **Step 6: Commit** `feat(dsh-plugin): expose terminal tools to Harness agents`

### Task 10: Один LLM на всё, навыки, панели и MCP

**Files:**
- Create: `dsh-ai-terminal/llm-bridge.js`, `dsh-ai-terminal/skills.js`, `dsh-ai-terminal/test/llm-bridge.test.mjs`
- Create: `terminal/mcp/server.go`, `terminal/mcp/server_test.go`
- Create: `.dsh/skills/trading-terminal/SKILL.md` (как пользоваться терминалом из Harness: порядок «бэктест → walk-forward → предложение»)
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
- Produces: провайдер навыков `ai-terminal`. Он отдаёт навыки из `<repo>/.dsh/skills/trading-*` и из каталога сгенерированных паттернов `<dataDir>/skills` (задача 19).
- Produces: клиентские поверхности:
  - вкладка правой панели «Терминал» (`sidebar.right.pane.tab`) со стаканом и позицией;
  - действие в шапке сессии «Отправить график в чат» — `insertQuote` с `ScreenContext` в виде JSON-цитаты;
  - пункт контекстного меню «Спросить о выделенном».

- [ ] **Step 1: Write the failing node tests:**
  - `llm bridge maps tool_calls both ways`: OpenAI `tools` → `ctx.llm.stream({tools})` → `finish` с `tool_calls` → OpenAI `choices[0].message.tool_calls`.
  - `llm bridge rejects missing token`.
- [ ] **Step 2: Write the failing Go tests** `TestMCPInitializeAndList` и `TestMCPToolCallRoundTrip`: JSON-RPC 2.0, `protocolVersion` из запроса клиента, результат `content:[{type:"text",text:<json>}]`.
- [ ] **Step 3: Implement** мост LLM, MCP-сервер, провайдер навыков и клиентские поверхности.
- [ ] **Step 4:** `make verify && node --test dsh-ai-terminal/test/` → exit 0.
- [ ] **Step 5: Manual check.** Из Claude Code: `claude mcp add --transport http terminal http://127.0.0.1:8090/mcp --header "X-Terminal-Token: $TERMINAL_API_TOKEN"`, затем `tools/list` показывает 14+ инструментов.
- [ ] **Step 6: Commit** `feat(dsh-plugin): share the Harness LLM, skills and an MCP endpoint with the terminal`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestChstoreInsertBatchesJSONEachRow` (`httptest`)
  - `TestChstoreQueryParams`
  - `TestRecorderWritesSnapshotThenLevels`
  - `TestRecorderMarksGapOnReconnect` (Review Focus 3): событие `Reconnected` клиента → строка `kind='gap'`, затем новый `snapshot`.
  - `TestRecorderKeepsMillis`
- [ ] **Step 2:** `go test ./terminal/chstore ./terminal/recorder -v` → FAIL.
- [ ] **Step 3: Implement.** `cmd/md-recorder` берёт параметры из env `TC_*`, `MD_CLICKHOUSE_URL`, `MD_SYMBOLS`; только read-only подписки.
- [ ] **Step 4:** `make verify` → exit 0.
- [ ] **Step 5: Live check** (с согласия пользователя в чате, окно демо-сервера): 10 минут по `TQBR:SBER`; `SELECT kind, count() FROM md.book_events GROUP BY kind` — есть `snapshot` и `level`. Результат — в `data.md` (**S**).
- [ ] **Step 6: Commit** `feat(recorder): record order-book events and trades to ClickHouse`

### Task 12: Новости TRANSAQ с моментом первого появления

**Files:**
- Modify: `terminal/recorder/recorder.go`, `terminal/recorder/schema.go`, `terminal/feed/live.go` (новости в ленту терминала)
- Test: `terminal/recorder/recorder_test.go`

**Interfaces:**
- Produces: `md.news(first_seen DateTime64(3,'UTC'), published DateTime64(3,'UTC'), news_id Int64, source LowCardinality(String), title String, body String, content_hash UInt64)`; `ReplacingMergeTree(first_seen) ORDER BY (news_id)`.
  - `first_seen` — момент получения рекордером: это и есть point-in-time.
- Produces: `feed.Event{Type:"news"}` и инструмент агента `get_news(symbol?, since, limit)`.

- [ ] **Step 1: Write the failing tests:**
  - `TestRecorderStoresNewsWithFirstSeen`: тело, пришедшее позже заголовка, не меняет `first_seen`.
  - `TestGetNewsToolFiltersBySince`.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(recorder): store TRANSAQ news with first-seen timestamps`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestVintageAsOfExcludesLaterRevision` (Review Focus 1): `httptest` ClickHouse с двумя релизами `unemployment_sa` за один месяц → на дату между релизами — первая версия.
  - `TestVintageLatestOnlyFlagged`.
  - `TestReleasesNormalizedToUTC`: `published_at DateTime('Europe/Moscow')` → UTC.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(vintage): point-in-time macro, release calendar and news access`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestReplayResetsBookAfterGap` (Review Focus 3)
  - `TestLimitFillRespectsQueue`
  - `TestMarketOrderWalksBook`
  - `TestLatencyDelaysVisibility`
  - `TestReplayDeterministic`: два прогона → одинаковый `Result`.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(lobreplay): event-driven order-book replay with queue-aware fills`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestWalkForwardNoLookahead`: в стратегию передаётся срез только train+test текущего окна; паника при доступе за пределы.
  - `TestDeflatedSharpePenalizesTrials`
  - `TestCompareStrategiesRanksByOOS`
  - `TestEventWindowAcrossUSDST` (Review Focus 2): релиз 8:30 ET 10.03 и 03.11 → окна 15:30 и 16:30 МСК соответственно.
  - Тесты стратегий на синтетическом тренде и флэте: на тренде есть сделки, на флэте их мало.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(research): trend strategies with walk-forward and deflated Sharpe`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestOracleRaisesSigmaBeforeKeyRateDecision`: синтетика, где вокруг решения ЦБ RV ×3 → `Multiplier > 2` в окне решения.
  - `TestOracleFitUsesOnlyDataBeforeAsOf`
  - `TestQLIKEBeatsHARonSynthetic`
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(volatility): event- and news-aware volatility oracle`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestDetectsSyntheticTWAP` (recall ≥ 0,8 на 20 seeds)
  - `TestDetectsSyntheticIceberg`
  - `TestDetectsSyntheticMarginLiquidation`
  - `TestNoFalsePositiveOnShuffledTape`: перемешанная лента → `Confidence < 0,3`.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(fingerprint): detect execution algorithms and margin-call liquidations`

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

- [ ] **Step 1: Write the failing tests:**
  - `TestResearchToolsRequireAsOf`: без `asOf` → ошибка с подсказкой.
  - `TestToolResultCompactUnder2KB`
  - `TestLedgerFindsRepeatedSequence`: 5 диалогов `get_history→run_backtest→compare_strategies` → `Hotspot` c `Calls=5`.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(agent): research tools with compact results and a token ledger`

---

## Фаза F. Самообучающийся контур

### Task 19: Агент пишет SKILL-паттерны по итогам бэктеста

**Files:**
- Create: `terminal/skills/{pattern.go,writer.go,promote.go,skills_test.go}`
- Create: `.dsh/skills/trading-pattern-author/SKILL.md` — как агент формулирует паттерн, какие доказательства прикладывает и чего не делает.
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

- [ ] **Step 1: Write the failing tests:**
  - `TestSkillPromotionRequiresOOS` (Review Focus 4)
  - `TestWriteDraftFrontmatterParsesInDSHFormat`: frontmatter читается тем же разбором, что и у навыков Harness (`name`/`description` обязательны).
  - `TestSkillHasNoSecretsOrAccounts`: значения из `TC_LOGIN`/`TC_PASSWORD` в тестовом окружении не появляются в файле.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(skills): draft and promote evidence-backed trading pattern skills`

### Task 20: Задачи на автоматизацию вычислений вместо токенов

**Files:**
- Create: `terminal/automation/{backlog.go,backlog_test.go}`
- Create: `.dsh/skills/trading-automation-author/SKILL.md`
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

- [ ] **Step 1: Write the failing tests:**
  - `TestProposeFromHotspotsRanksByTokens`
  - `TestWriteBacklogIsIdempotent`: повторная запись не дублирует задачи с тем же `ID`.
  - `TestBacklogPreservesManualText`: текст вне маркеров не меняется.
- [ ] **Step 2–4:** red → implement → `make verify`.
- [ ] **Step 5: Commit** `feat(automation): turn repeated agent computations into automation tasks`

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

- [ ] **Step 1: Write the failing node tests:**
  - `nightly workflow stops on token budget`
  - `schedule is not created when autonomy.enabled is false`
  - `kill switch cancels the running workflow and stop_autonomy`
- [ ] **Step 2–4:** red → implement → `node --test dsh-ai-terminal/test/ && make verify`.
- [ ] **Step 5: Manual check.** Ручной запуск workflow из Harness на офлайн-данных. В сессии появляется сводка, в `<dataDir>/skills` — черновики, в `automation-backlog.md` — задачи. Скриншот в `docs/ai-terminal/`.
- [ ] **Step 6: Commit** `feat(dsh-plugin): scheduled autonomous research workflow with token budget`

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
