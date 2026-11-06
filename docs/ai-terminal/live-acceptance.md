# Live/Manual acceptance AI-терминала (план 2026-11-05)

Дата: 2026-11-06. Согласие пользователя на живые проверки получено; демо-креды — `~/re/demo.env`
(вне git), dev-ClickHouse — `clickhouse-import-rosstat` (`127.0.0.1:8123`, MCP-конфиг проекта
`.kimi-code/mcp.json` указывает на ту же БД). Секреты в логах/документах не печатаются.

## Сводка 8 шагов

| Шаг плана | Статус | Доказательство | Осталось |
|---|---|---|---|
| T9 Step 5 (DSH-карточка `terminal_run_backtest`) | **I** | инструменты Harness + toolview проверены node-тестами и MCP-вызовами; живой DSH не запускался | перезапуск DSH с текущим плагином |
| T10 Step 5 (Claude Code MCP) | **S** | `claude mcp add --transport http terminal http://127.0.0.1:<port>/mcp --header X-Terminal-Token: …` → `claude mcp list` = ✔ Connected; MCP `initialize`/`tools/list` = 32 инструмента; `tools/call get_quote` → `result.symbol=TQBR:SBER` | LLM-вызов инструмента из Claude Code (истекла OAuth-сессия Claude Code) — не блокер |
| T11 Step 5 (10 мин записи → `md.book_events`) | **S (схема) / live в окне демо** | `EnsureSchema` создал `md.*` на живом ClickHouse, типы колонок совпадают с планом | фоновая проверка `tools/demo-live-check.sh` ждёт окно 09:50 МСК |
| T12 (news live, вне 8 шагов) | live в окне демо | `md.news` создана; `first_seen ≤ now`, `published` проверяются тем же инструментом | там же |
| T13/T16 (vintage/оракул live, вне 8 шагов) | **S** | живые витрины: `AsOf` 107 точек (review focus 1), `Releases` 260, `News` 773, latest-only/publication paths; `Fit/Forecast` на реальных релизах/новостях (87 новостей, 276 релизов) | — |
| T21 Step 5 (ночной workflow в DSH) | **I** | workflow-body, schedule-shape, budget, kill-switch проверены node-тестами на fake-сервисах | перезапуск DSH; `make terminal_smoke`/ручной запуск workflow |
| T22 Step 5 (dry-run бот 30 мин) | live в окне демо | код и 11 тестов зелёные; REST/SSE ботов — offline smoke | фоновая проверка `tools/demo-live-check.sh` |
| T24 Step 5 (промпт в DSH) | **I (путь инструмента — S)** | offline: `batch_backtest` invoke → 3 ряда, `small_sample`, artifact с charts; `backtest_rules` invoke | промпт в живом DSH после перезапуска |
| T25 Step 5 (`npx skills add`) | **S (установка) / I (промпт)** | `npx -y skills add <repo> --skill trading-terminal --agent claude-code -y` → `./.claude/skills/trading-terminal` (+ `scripts/terminal.sh`), «Installed 1 skill» | прогон промпта в Claude Code (OAuth) |
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