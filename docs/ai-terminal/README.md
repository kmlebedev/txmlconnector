# AI-терминал Finam/MOEX — нативный терминал с AI-агентом

Один Go-бинарник (`cmd/terminal`) без DLL, Wine и CGO: рыночные данные и история берутся из
нативного клиента TRANSAQ (`native.Client`), интерфейс — SPA-аналог терминала Finam, внутри —
AI-агент, который **без MCP** вызывает инструменты терминала (реальный бэктест на истории,
технические подсказки, стакан, портфель, бумажные заявки) и может автономно вести стратегию на
бумажном счёте.

Скриншоты: [основной экран](screenshot-main.png) · [AI-агент: чат, бэктест, overlay](screenshot-ai.png) ·
[автономия на бумажном счёте](screenshot-autonomy.png) · [вкладка «Аналитика»](screenshot-analytics.png) ·
[1440×900](screenshot-main-1440.png).

## Документы

| Документ | Что внутри |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | замороженные контракты итерации 1 + §15 «Пакеты фаз C–G» (API, SSE, MCP, инструменты) |
| [data.md](data.md) | ClickHouse, рекордер `md.*`, винтажи, новости |
| [research.md](research.md) | walk-forward, deflated Sharpe/PBO, оракул волатильности, контракт T18 (`asOf` обязателен, компактный ответ + `artifactId`) |
| [fingerprint.md](fingerprint.md) | отпечатки исполнения роботов и replay стакана |
| [dsh-integration.md](dsh-integration.md) | DSH-плагин: общий LLM, провайдер навыков, MCP, панели, ночные исследования |
| [PROGRESS.md](PROGRESS.md) | статус фаз, коммиты, ревью, что не проверено (**I**) |
| [automation-backlog.md](automation-backlog.md) | сгенерированный бэклог задач автоматизации |
| [`skills/README.md`](../../skills/README.md) | переносимые навыки `npx skills add` для Cursor/Claude Code/Qwen/Codex |

## Быстрый старт (офлайн-демо, без ключей и сети)

```bash
# в корне репозитория (ветка feat/ai-terminal)
make terminal            # соберёт bin/terminal (или: go build -o bin/terminal ./cmd/terminal)
./bin/terminal -mode offline
# открыть http://127.0.0.1:8090
```

Offline-режим использует встроенный снапшот реальных данных демо-сервера TRANSAQ
(`terminal/seeddata/data/seed.json.gz`: 11 бумаг TQBR, свечи 1м/5м/15м/1ч/1д, стаканы, котировки).
Сеть не нужна, секреты не нужны. AI-агент в этом режиме работает локальным аналитиком (LLM не
настроен) — он выполняет те же инструменты и показывает те же карточки, что и LLM-режим.

## Режимы

| Режим | Запуск | Данные | Торговля |
|---|---|---|---|
| `offline` | `-mode offline` (по умолчанию, если нет кредов) | встроенный seed с демо-сервера | бумажная |
| `live` | `-mode live` + env кредов | живой TRANSAQ/Finam через `native.Client` | бумажная (по умолчанию) |
| `auto` | `-mode auto` | live при наличии кредов, иначе offline | бумажная |

Креды живого режима (значения никогда не логируются и не пишутся в снапшот):

```bash
export TRANSAQ_ADDR=tr1-demo5.finam.ru:3939     # или TC_HOST/TC_PORT
export TRANSAQ_LOGIN=...                        # fallback TC_LOGIN
export TRANSAQ_PASSWORD=...                     # fallback TC_PASSWORD (лучше set -a; . ~/re/demo.env; set +a)
./bin/terminal -mode live
```

Демо-сервер Finam работает 09:50–23:50 МСК в будни. Реальные ордера **выключены по умолчанию**:
`POST /api/orders` с `"confirm":"LIVE"` возвращает `403 live_trading_disabled`, пока не заданы
`-live-trading` и `TERMINAL_LIVE_TRADING=1`; маршрутизация реальных ордеров в итерации 1 —
roadmap (честный `501 not_implemented`).

## AI-агент и LLM

Без LLM работает детерминированный **локальный аналитик**: разбирает запрос на русском (тикер,
период, стратегия, «запусти/останови»), вызывает реальные инструменты
(`compare_strategies → get_market_hints → run_backtest → start_autonomy`) и отвечает с метриками и
рисками. Это демо-режим по умолчанию.

LLM-режим — любой OpenAI-compatible провайдер (провайдер терминала, не MCP):

```bash
export TERMINAL_LLM_BASE_URL=https://api.deepseek.com/v1
export TERMINAL_LLM_API_KEY=...
export TERMINAL_LLM_MODEL=deepseek-chat
./bin/terminal -mode offline          # /api/health покажет llm.enabled=true
```

Для E2E-демонстрации без ключа есть скриптовый OpenAI-compatible сервер:

```bash
go run ./tools/llm-stub -addr 127.0.0.1:8099
TERMINAL_LLM_BASE_URL=http://127.0.0.1:8099/v1 TERMINAL_LLM_API_KEY=stub \
  TERMINAL_LLM_MODEL=llm-stub ./bin/terminal -mode offline -addr 127.0.0.1:8093
```

Встроенные инструменты агента (прямые Go-вызовы в процессе; полный JSON-каталог —
`GET /api/agent/tools`, контракт — [ARCHITECTURE §9.1](ARCHITECTURE.md#91-инструменты-агента-tool-calling-без-mcp)):

- базовые (14): `list_instruments`, `get_market_hints`, `get_history`, `run_backtest`,
  `compare_strategies`, `get_quote`, `get_portfolio`, `get_strategies`, `place_order`,
  `cancel_order`, `start_autonomy`, `stop_autonomy`, `get_agent_status`, `get_backtest_history`;
- исследования (фазы E–G): `backtest_rules`, `batch_backtest`, `walk_forward`, `vol_forecast`,
  `vol_events`, `detect_execution_algos`, `lob_backtest`, `get_news`;
- самообучение: `draft_skill_pattern`, `list_skill_patterns`, `propose_automation`;
- rule-боты: `bot_create_from_backtest`, `bot_check`, `bot_promote`, `bot_start`, `bot_stop`,
  `bot_status`, `bot_journal`.

Те же инструменты доступны внешним агентам через MCP `POST /mcp` (streamable HTTP, тот же токен
`X-Terminal-Token`) и через общий invoke `POST /api/agent/tools/{name}/invoke`.

**Автономия** — paper-replay: агент выбирает стратегию по бэктесту и ведёт её по историческим барам
со скоростью 1–60 бар/с с бюджетом и риск-лимитами (max position/order, stop-loss, take-profit,
дневной лимит убытка, лимит сделок/час), kill-switch и автостопом. Реальные ордера в автономии
итерации 1 не отправляются (`live:true` → ошибка).

## UI

- Список инструментов со спарклайнами, свечной график с объёмом, стакан, лента сделок, тикет,
  портфель/заявки/история/аналитика — светлая тема, жёлтый акцент (аналог Finam).
- AI-панель: чат, карточки tool-calls, карточка бэктеста (метрики + equity + «Показать на графике»),
  панель автономии (стратегия, параметры, бюджет, риск-лимиты, скорость, старт/стоп, PnL, лог).
- `?mock=1` — режим моков для разработки интерфейса без бэкенда.

## Встраивание в DeepSeek Harness (плагин)

Терминал встраивается в Web UI Harness как **основная панель**: плагин
[`dsh-ai-terminal`](../../dsh-ai-terminal/README.md) добавляет иконку «AI-терминал» в левую панель,
поднимает Go-бэкенд (по умолчанию `bin/terminal`) и проксирует его SPA/API/SSE под origin Harness
(`/ai-terminal/`), поэтому отдельный порт в браузере и CORS не нужны. Панель оформлена токенами
темы Harness (en/ru/zh), показывает статус бэкенда и кнопки «Обновить / Перезапустить / Открыть в
браузере». Установка — `plugin_manager install_bundle` с `target = <repo>/dsh-ai-terminal`;
конфигурация (`mode`, `port`, `repoDir`, `binaryPath`, `autoStart`) описана в README плагина.
После изменения `terminal/web/**` нужно пересобрать `bin/terminal` (`make terminal`), потому что
SPA вшивается в Go-бинарь.

## HTTP API

REST + SSE, контракт — [ARCHITECTURE §10](ARCHITECTURE.md#10-пакет-terminalapi-httpjson-sse).
Ключевые точки:

```bash
curl http://127.0.0.1:8090/api/health
curl http://127.0.0.1:8090/api/instruments
curl 'http://127.0.0.1:8090/api/instruments/TQBR:SBER/candles?period=300&count=200'
curl -X POST http://127.0.0.1:8090/api/backtest \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"TQBR:SBER","periodSec":300,"strategy":"sma_cross","count":700}'
curl -X POST http://127.0.0.1:8090/api/agent/chat \
  -H 'Content-Type: application/json' -d '{"message":"протестируй лучшую стратегию на SBER"}'
```

Research-инструменты и rule-боты (T18/T22): у research-вызовов `asOf` обязателен, полный результат
приходит как артефакт, а модель получает компактную сводку с `artifactId`:

```bash
curl -X POST http://127.0.0.1:8090/api/agent/tools/walk_forward/invoke \
  -H 'Content-Type: application/json' \
  -d '{"args":{"symbol":"TQBR:SBER","strategy":"sma_cross","trainBars":200,"testBars":100,"asOf":"2026-11-05T00:00:00Z"}}'
curl -X POST http://127.0.0.1:8090/api/agent/tools/vol_forecast/invoke \
  -H 'Content-Type: application/json' \
  -d '{"args":{"symbol":"TQBR:SBER","horizon":"8h","asOf":"2026-11-05T00:00:00Z"}}'
curl http://127.0.0.1:8090/api/bots
curl -X POST http://127.0.0.1:8090/api/bots -H 'Content-Type: application/json' \
  -d '{"symbol":"TQBR:SBER","periodSec":300,"qty":10,"rules":{"entry":[{"left":"close","op":">","right":"0"}],"exit":[{"left":"close","op":"<","right":"0"}]}}'
curl -X POST http://127.0.0.1:8090/api/bots/{id}/check
```

Внешние MCP-клиенты (Claude Code и другие) используют `POST /mcp` — JSON-RPC 2.0
(`initialize` → `tools/list` → `tools/call`) с тем же токеном; контракт — [ARCHITECTURE §10](ARCHITECTURE.md#10-пакет-terminalapi-httpjson-sse).

Если задан `TERMINAL_API_TOKEN` (так делает плагин DSH), добавьте к запросам
`-H "X-Terminal-Token: $TERMINAL_API_TOKEN"`; `/api/health` открыт всегда. Без токена API
защищён проверками Host (только loopback), Origin (свой Origin или `-allowed-origins`) и
Content-Type (`application/json` для запросов с телом); подробно —
[ARCHITECTURE §10.4](ARCHITECTURE.md#104-безопасность).

## Проверки и данные

```bash
export GOCACHE="$PWD/.cache/go-build"
make verify                       # gofmt/vet/build/кроссы/race
go test -race ./terminal/... ./cmd/terminal/ ./tools/llm-stub/
```

Обновление офлайн-снапшота с демо-сервера (read-only, секреты из env):
[`tools/terminal-seed`](../../tools/terminal-seed/README.md).

## End-to-end offline smoke (REST + tools + bots + MCP + SSE)

```bash
make terminal_smoke                 # build + полный offline acceptance-прогон, PASS/FAIL по шагам, exit code
SMOKE_KEEP=1 make terminal_smoke    # сохранить лог бэкенда после прогона
```

`tools/terminal-smoke.sh` поднимает `bin/terminal -mode offline` (встроенный seed, только loopback,
без сети/DLL/ключей) на свободном порту с тестовым `TERMINAL_API_TOKEN` и
`TERMINAL_INSTANCE_ID`, ждёт `/api/health` и проверяет замороженный контракт:

- токен-guard: `/api/health` без токена — 200, `/api/instruments`, `/api/events` и `/mcp` без
  токена — 401;
- `/api/instruments`, `POST /api/backtest` (`sma_cross`, 300 баров);
- каталог `/api/agent/tools` (30+ инструментов) и invoke
  `POST /api/agent/tools/get_quote/invoke` → `result.symbol`;
- rule-боты: `POST /api/bots` из `rules.Spec`, `GET /api/bots`, `POST /api/bots/{id}/check`;
- MCP streamable-HTTP: `initialize` + `tools/list` (счётчик совпадает с каталогом агента);
- SSE `/api/events`: первые кадры `quote/portfolio/status`;
- корректное завершение по SIGTERM (exit code 0, лог «завершение работы»).

Скрипт печатает `PASS`/`FAIL` по каждому шагу и возвращает ненулевой exit code при любом падении;
живой сервер, креды и сеть не нужны. Требования: `bash`, `curl`, `python3`.

## Статус проверки (2026-11-05, docs-sync 2026-11-06)

- **Фазы C–G (2026-11-06, ветка `plan/ai-terminal-a`)**: MCP `/mcp`, рекордер/винтажи,
  walk-forward/батчи/оракул/отпечатки, SKILL-паттерны, ledger и бэклог автоматизации, rule-боты —
  проверены unit/race-тестами, `make verify` зелёный на коммитах фаз (`6248d6b`, `0f16134`,
  `29e60fa`, `adedeac`, `ebeaddb` и др.). Живые прогоны (ClickHouse, демо-сервер TRANSAQ, живой DSH/браузер,
  `npx skills add` в чистом Claude Code/Cursor) не выполнялись — **I** (см. PROGRESS.md).

- `gofmt` пуст, `go vet ./...`, `go build ./...`, `go test -race ./...` — зелёные на HEAD ветки
  `feat/ai-terminal`; кросс-сборки терминала `CGO_ENABLED=0 linux/amd64` и `darwin/arm64` — OK.
- **Офлайн-демо**: acceptance-прогон UI через CDP на `:8090` — 11 инструментов, реальный ответ
  локального аналитика (3 tool-call, карточка бэктеста, рекомендация), overlay «Показать на
  графике», запуск/остановка автономии (журнал заявок/исполнений, PnL, MTM на завершении), вкладка
  «Аналитика», JS-консоль без ошибок; HTTP/SSE-эндпоинты, paper-заявки и портфель проверены.
- **Живой read-only режим**: `-mode live` против демо-сервера TRANSAQ — `connected=true`,
  живые котировки/стаканы (source=live), история 5м/700 баров, `serverTime`=UTC,
  `exchangeTZ=Europe/Moscow`; реальные ордера заблокированы (403/501).
- Seed снят с демо-сервера 2026-11-05 (`tools/terminal-seed`), 11 бумаг TQBR.
- Независимое ревью кода второй моделью — см. отчёт в задаче; LLM-провайдер проверен через
  `tools/llm-stub` (реальный внешний провайдер в демо не подключался — нужен ключ пользователя).

## Ограничения итерации 1 (честно)

- Реальные торги не маршрутизируются (paper-only); live-автономия по стакану — roadmap.
- Автономия работает на replay исторических баров, а не на живом потоке.
- Модель T+1/маржи и корпоративные действия не моделируются; комиссия фиксированная (5 б.п.).
- Seed — данные демо-сервера; справочные поля (например, лот) такие, какие отдал демо-каталог.
- API слушает loopback и защищён `Guard`: Host (только loopback), Origin для небезопасных
  методов, `Content-Type: application/json` для запросов с телом и токен `TERMINAL_API_TOKEN`
  для `/api/*` кроме `/api/health` (плагин DSH подставляет токен сам); MCP `/mcp` — под тем же
  Guard. Запускать всё равно только локально — см. [ARCHITECTURE §10.4](ARCHITECTURE.md#104-безопасность).
- Паттерны из `terminal/skills` видны агенту только в статусе `validated`: черновики лежат в
  каталоге `<dataDir>/skills` вне git и доступны лишь инструменту `list_skill_patterns`; в
  репозиторий паттерн попадает только через PR человека.
- Rule-боты (`terminal/bot`) исполняются в `check`/`dry_run`/`paper`; live-стадия отключена
  (`ErrLiveDisabled`), реальные заявки недоступны.

Архитектура и замороженные контракты: [ARCHITECTURE.md](ARCHITECTURE.md).
