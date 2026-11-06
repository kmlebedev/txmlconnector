# dshtrading-connector-transaq

Cordis-плагин, который подключает Go-шлюз `/api/gw/v1/*` (`terminal/api/gateway.go`, контракт —
[`docs/ai-terminal/gateway-v1.md`](../docs/ai-terminal/gateway-v1.md)) к хосту
[dsh-trading](https://github.com/zhu1090093659/dsh-trading) как рынок `ru`, провайдер `finam`.
Решение и обоснование — [`docs/ai-terminal/adr-dsh-trading.md`](../docs/ai-terminal/adr-dsh-trading.md) (вариант D).

Пакет без зависимостей (`"dependencies": {}`, `"private": true`), код dsh-trading не копируется и не
импортируется: совместимость только структурная (лицензия dsh-trading — PolyForm Noncommercial 1.0.0).

## Что делает

- Регистрирует `MarketDataService` (тикер, свечи, стакан, сделки, список инструментов, `subscribeTicker`
  поллингом) и `TradeService`: read-only просмотр бумажного счёта (балансы, позиции, заявки, сделки) и dry-run проверка заявок (см. «Ограничения») в реестрах
  `tradingMarketDataRegistry` / `tradingTradeRegistry` под ключом `ru` / `finam`.
- Регистрирует агентские инструменты `ru_get_ticker`, `ru_get_klines`, `ru_get_orderbook`,
  `ru_get_trades`, `ru_list_instruments`, `ru_get_paper_account`.
- Символы: канонический `SECCODE.BOARD` (`SBER.TQBR`); шлюз использует `BOARD:SECCODE` (`TQBR:SBER`).
  Голый `SBER` разрешается в единственный такой `SECCODE`; при нескольких площадках предпочитается
  `TQBR`, иначе `TRADING_UNSUPPORTED_SYMBOL` со списком кандидатов.
- Объёмы: шлюз отдаёт лоты, коннектор переводит в штуки (10 лотов SBER при лоте 10 = `amount: 100`).
- Ответы несут `source: "live"` или `"seed"` (офлайн-снимок, не реальное время).

## Требования

Запущенный `bin/terminal` (`make terminal`) и токен API в окружении:

```bash
export TERMINAL_API_TOKEN="$(openssl rand -hex 16)"
bin/terminal -mode offline -addr 127.0.0.1:18090   # или -mode live (нужен доступ к TRANSAQ)
```

Токен на каждый запрос сначала запрашивается у сервиса `credentials` хоста, при его отсутствии читается из переменной окружения с именем `tokenRef`;
каждый запрос и не попадает в логи и тексты ошибок. Переменные `TC_*` / `TRANSAQ_*` коннектор не читает.

## Подключение

Строка профиля Cordis:

```yaml
- id: dsh-trading-ru-connector-transaq
  name: '@local/dshtrading-connector-transaq'
  config: { enabled: true, gatewayUrl: 'http://127.0.0.1:18090', tokenRef: TERMINAL_API_TOKEN }
```

| Параметр | По умолчанию | Описание |
|---|---|---|
| `enabled` | `true` | `false` — плагин ничего не регистрирует |
| `gatewayUrl` | `http://127.0.0.1:18090` | только `http://` и loopback (`127.0.0.1`, `localhost`, `[::1]`) |
| `tokenRef` | `TERMINAL_API_TOKEN` | имя переменной окружения с токеном шлюза |
| `timeoutMs` | `5000` | таймаут запроса к шлюзу; при превышении `TRADING_NETWORK` |
| `pollMs` | `2000` | период опроса для `subscribeTicker` |

Ошибки: `TRADING_AUTH_FAILED` (неверный токен), `TRADING_NETWORK` (шлюз недоступен),
`TRADING_UNSUPPORTED_SYMBOL`. `subscribeTicker` продолжает опрос и восстанавливается после рестарта
шлюза без перезапуска хоста.

## Ограничения

- Вкладки `ru` в GUI dsh-trading нет, пока апстрим не откроет список рынков (ADR, «Что важно», п. 3):
  сервис регистрируется, но доступен через агента и API, а не через вкладку.
- Реального размещения заявок нет ни live, ни paper: шлюз v1 только GET. `placeOrder` при
  `dryRun !== false` лишь локально проверяет заявку (кратность лоту, шаг цены, side/type) и возвращает
  синтетический ордер `dry-<uuid>` с `dryRun: true`; `cancelOrder` принимает только такие `dry-` id.
  Ничего не уходит в шлюз, на биржу или в бумажный счёт. `placeOrder` с `dryRun: false` всегда
  отклоняется с `TRADING_LIVE_TRADING_DISABLED`. Инструменты `ru_place_order` / `ru_cancel_order` не
  регистрируются намеренно (approval-gate dsh-trading не покрывает `ru`).
- К MOEX агент обращается только через `ru_*`.

## Тесты и приёмка

```bash
make connector-test        # node --test dshtrading-connector-transaq/test/
```

Офлайн-приёмка (выполнена 2026-11-06): `apply()` с фейковым ctx против `bin/terminal -mode offline`
вернул `SBER.TQBR`, `source: "seed"`, 5 часовых свечей и стакан. Проверка в реальном хосте dsh-trading
и живая проверка на демо-сервере **не выполнялись** (см. `docs/ai-terminal/PROGRESS.md`).
