# AI-терминал Finam/MOEX — нативный терминал с AI-агентом

Один Go-бинарник (`cmd/terminal`) без DLL, Wine и CGO: рыночные данные и история берутся из
нативного клиента TRANSAQ (`native.Client`), интерфейс — SPA-аналог терминала Finam, внутри —
AI-агент, который **без MCP** вызывает инструменты терминала (реальный бэктест на истории,
технические подсказки, стакан, портфель, бумажные заявки) и может автономно вести стратегию на
бумажном счёте.

Скриншоты: [основной экран](screenshot-main.png) · [AI-агент: чат, бэктест, overlay](screenshot-ai.png) ·
[автономия на бумажном счёте](screenshot-autonomy.png) · [вкладка «Аналитика»](screenshot-analytics.png) ·
[1440×900](screenshot-main-1440.png).

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

Встроенные инструменты агента (все — прямые Go-вызовы в процессе, без MCP; см.
[ARCHITECTURE §9](ARCHITECTURE.md#9-пакет-terminalagent-ai-агент-tool-loop-автономия)):
`list_instruments`, `get_market_hints`, `get_history`, `run_backtest`, `compare_strategies`,
`get_quote`, `get_portfolio`, `get_strategies`, `place_order`, `cancel_order`, `start_autonomy`,
`stop_autonomy`, `get_agent_status`, `get_backtest_history`.

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

## Проверки и данные

```bash
export GOCACHE="$PWD/.cache/go-build"
make verify                       # gofmt/vet/build/кроссы/race
go test -race ./terminal/... ./cmd/terminal/ ./tools/llm-stub/
```

Обновление офлайн-снапшота с демо-сервера (read-only, секреты из env):
[`tools/terminal-seed`](../tools/terminal-seed/README.md).

## Статус проверки (2026-11-05)

- `gofmt` пуст, `go vet ./...`, `go build ./...`, `go test -race ./...` — зелёные на HEAD ветки
  `feat/ai-terminal`; кросс-сборки терминала `CGO_ENABLED=0 linux/amd64` и `darwin/arm64` — OK.
- **Офлайн-демо**: acceptance-прогон UI через CDP на `:8090` — 11 инструментов, реальный ответ
  локального аналитика (3 tool-call, карточка бэктеста, рекомендация), overlay «Показать на
  графике», запуск/остановка автономии (7 строк лога, PnL), вкладка «Аналитика», JS-консоль без
  ошибок; HTTP/SSE-эндпоинты, paper-заявки и портфель проверены.
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
- Сервер без аутентификации слушает loopback; запускать только локально.

Архитектура и замороженные контракты: [ARCHITECTURE.md](ARCHITECTURE.md).
