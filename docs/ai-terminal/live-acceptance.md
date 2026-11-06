# Live/Manual acceptance AI-терминала (план 2026-11-05)

Дата: 2026-11-06. Согласие пользователя на живые проверки получено; демо-креды — `~/re/demo.env`
(вне git), dev-ClickHouse — `clickhouse-import-rosstat` (`127.0.0.1:8123`, MCP-конфиг проекта
`.kimi-code/mcp.json` указывает на ту же БД). Секреты в логах/документах не печатаются.

## Сводка 8 шагов

| Шаг плана | Статус | Доказательство | Осталось |
|---|---|---|---|
| T9 Step 5 (DSH-карточка `terminal_run_backtest`) | **I** | инструменты Harness + toolview проверены node-тестами и MCP-вызовами; живой DSH не запускался | перезапуск DSH с текущим плагином |
| T10 Step 5 (Claude Code MCP) | **S** | `claude mcp add --transport http terminal http://127.0.0.1:<port>/mcp --header X-Terminal-Token: …` → `claude mcp list` = ✔ Connected; MCP `initialize`/`tools/list` = 32 инструмента; `tools/call get_quote` → `result.symbol=TQBR:SBER` | LLM-вызов инструмента из Claude Code (истекла OAuth-сессия Claude Code) — не блокер |
| T11 Step 5 (10 мин записи → `md.book_events`) | **S (схема + E2E пайплайн локально) / live в окне демо** | `EnsureSchema` создал `md.*` на живом ClickHouse, типы колонок совпадают с планом | фоновая проверка `tools/demo-live-check.sh` ждёт окно 09:50 МСК |
| T12 (news live, вне 8 шагов) | live в окне демо | `md.news` создана; `first_seen ≤ now`, `published` проверяются тем же инструментом | там же |
| T13/T16 (vintage/оракул live, вне 8 шагов) | **S** | живые витрины: `AsOf` 107 точек (review focus 1), `Releases` 260, `News` 773, latest-only/publication paths; `Fit/Forecast` на реальных релизах/новостях (87 новостей, 276 релизов) | — |
| T21 Step 5 (ночной workflow в DSH) | **I** | workflow-body, schedule-shape, budget, kill-switch проверены node-тестами на fake-сервисах | перезапуск DSH; `make terminal_smoke`/ручной запуск workflow |
| T22 Step 5 (dry-run бот 30 мин) | live в окне демо | код и 11 тестов зелёные; REST/SSE ботов — offline smoke | фоновая проверка `tools/demo-live-check.sh` |
| T24 Step 5 (промпт в DSH) | **I (путь инструмента — S)** | offline: `batch_backtest` invoke → 3 ряда, `small_sample`, artifact с charts; `backtest_rules` invoke | промпт в живом DSH после перезапуска |
| T25 Step 5 (`npx skills add`) | **S (установка + промпт через Kimi Code) / I (Claude Code-промпт)** | `npx -y skills add <repo> --skill trading-terminal --agent claude-code -y` → `./.claude/skills/trading-terminal` (+ `scripts/terminal.sh`), «Installed 1 skill» | прогон промпта в Claude Code (OAuth) |
| T26 Step 5 (7-шаговый сценарий бота) | **I** | relay/UI проверены node-тестами (fake ctx/agents/SSE) | перезапуск DSH и живой сценарий |

## Команды живой приёмки

```bash
# офлайн end-to-end (без сети/DLL):
make terminal_smoke            # 37/37 checks

# живые dev-витрины ClickHouse (согласие не требуется, только SELECT):
TXML_IT_CLICKHOUSE=1 TXML_CLICKHOUSE_URL=http://localhost:8123 \
  go test ./terminal/vintage ./terminal/volatility -run Live -v

# самопроверка инструмента живой приёмки (окно демо не нужно):
TERMINAL_LIVE_ACCEPTANCE=1 DEMO_CHECK_SELFTEST=1 make terminal_live

# полная живая приёмка T11/T12/T22 в окне демо-сервера (долго = окно + ~45 мин):
TERMINAL_LIVE_ACCEPTANCE=1 make terminal_live      # пишет .re-run/live/live-summary-*.json
# повтор без ожидания окна (если рынок уже открыт):
DEMO_CHECK_NO_WAIT=1 TERMINAL_LIVE_ACCEPTANCE=1 make terminal_live
```

## T10: Claude Code ↔ MCP (транскрипт)

```
bin/terminal -mode offline -addr 127.0.0.1:18091   # TERMINAL_API_TOKEN=… TERMINAL_INSTANCE_ID=…
claude mcp add --transport http terminal http://127.0.0.1:18091/mcp --header "X-Terminal-Token: …"
claude mcp list   # → terminal: http://127.0.0.1:18091/mcp (HTTP) - ✔ Connected
# MCP initialize → protocolVersion 2025-03-26, serverInfo ai-terminal; tools/list → 32 tools;
# tools/call get_quote {symbol:TQBR:SBER} → result.symbol=TQBR:SBER, last/bid/ask/book
```

Ограничение: OAuth-сессия Claude Code истекла, поэтому LLM-вызов инструмента из чата Claude Code не
выполнялся; транспорт, авторизация и JSON-RPC проверены напрямую.

## Живой DSH-прогон (T9/T21/T24/T26): что нужно сделать

Профиль DSH `~/.dsh/profiles/desktop/package.json` линкует `@local/dsh-ai-terminal` на старый
worktree `.worktrees/ai-terminal/dsh-ai-terminal` (код PR #60). Для проверки смёрженного кода:

1. В `~/.dsh/profiles/desktop/package.json` заменить зависимость на
   `"@local/dsh-ai-terminal": "link:/Users/whitefox/GolandProjects/txmlconnector/dsh-ai-terminal"`.
2. `pnpm install` в каталоге профиля.
3. Перезапустить DSH (текущая сессия завершится).
4. Проверить:
   - T9: в чате попросить бэктест — карточка `terminal_run_backtest` с equity;
   - T24: промпт «RSI 14 на GAZP, LKOH, SBER, сравни с buy & hold» — таблица, график с заливкой позиций, `small_sample`;
   - T21: панель «Автономия» — расписание, «Запустить сейчас», бюджет токенов, «Стоп»;
   - T26: сценарий «бэктест → dry-run → бумага → kill-switch», карточки ботов в чате.

Скриншоты живых проверок не создавались/не фальсифицировались; статусы I в этой таблице — честные.
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
## Живые результаты в DSH после исправления (2026-11-06, MCP-инструменты плагина доступны сессии)

После успешного перезапуска DSH плагин активен: `terminal_*`-инструменты доступны агенту, backend
`127.0.0.1:18090` жив (instanceId, `llm.enabled=true`). Проверено вызовами из живой DSH-сессии:

| Проверка | Результат |
|---|---|
| T9: `terminal_get_quote` / `terminal_run_backtest` (TQBR:SBER, sma_cross 5м) | **S**: котировка и полный artifact бэктеста (metrics/equity, доходность −0.17%, 4 сделки); карточка рендерится toolview-компонентом |
| T24: `terminal_batch_backtest` (GAZP/SBER/LKOH, sma_cross) | **S**: таблица 3 строк, buy&hold, warnings `small_sample` для малых выборок, artifact с графиками |
| T26: `terminal_bot_create_from_backtest` → `bot_check` → `bot_status` → `bot_start(dry_run)` | **S (частично)**: бот `bot_0001` создан из rules.Spec (stage=check), check вернул `closed_bar` (close=293.22, rsi=0.4378), статус-список работает; `bot_start` вернул **proposal** `prop_000001` (pending) — мутирующее действие ждёт подтверждения человека; фактический dry-run требует живого потока баров (демо-окно) |
| T21: `terminal_nightly_research` | **I (ограничение сборки DSH)**: tool доступен, но возвращает `workflowEngine Harness недоступен` — shipped desktop/web профили не монтируют сервис workflowEngine; плагин деградирует gracefully, DSH не падает. Живой запуск workflow в этой сборке невозможен без установки bundle `@deepseek-ai/dsh-workflow` |

Остаются: T11/T12/T22 — окно демо-сервера (фоновая приёмка перезапущена, ждёт 09:55 МСК);
T26 dry-run — подтверждение proposal человеком + живой поток; T21 — нужен workflowEngine bundle.