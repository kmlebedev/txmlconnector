# Gateway v1 (`/api/gw/v1/*`)

**v1 frozen; breaking change ⇒ /v2.** Статус полей: **I** (вывод из кода) до живой проверки на демо-сервере.

## Общие правила

- Только `GET`. Размещение заявок через gateway отсутствует.
- Токен: заголовок `X-Terminal-Token` (как у остального `/api/*`, кроме `/api/health`).
- Цены — десятичные строки с ровно `decimals` знаками после точки (`"0.0001"`, `"1"`).
- Время — epoch миллисекунды UTC (`0` для нулевого времени).
- Ошибки — существующий конверт `{"error":{"code","message"}}`.
- Символ gateway: `BOARD:SECCODE` (`TQBR:SBER`).

## GET /api/gw/v1/info (I)

```json
{"version":"1","market":"ru","provider":"finam","mode":"live|offline",
 "connected":true,"source":"live|seed","serverTime":1730823840000,
 "trading":{"paper":true,"live":false},
 "intervals":[{"interval":"1m","periodSec":60,"maxBars":5000}]}
```

- `source` = `seed` при `mode == offline`, иначе `live`.
- `trading.live` всегда `false`, даже при `-live-trading`.
- `intervals`: `1m`(60), `5m`(300), `15m`(900), `1h`(3600), `1d`(86400); `maxBars` — максимум свечей за запрос.

## GET /api/gw/v1/instruments (I)

```json
{"instruments":[{"symbol":"TQBR:SBER","secCode":"SBER","board":"TQBR",
 "name":"...","shortName":"...","currency":"RUB","decimals":2,
 "lotSize":10,"minStep":"0.01"}]}
```

`lotSize` — акций в лоте; `minStep` форматируется с `decimals` знаками.

## Единицы объёма: лоты (I)

Все поля `*Lots` / `lots` — лоты TRANSAQ, делений на `lotSize` gateway не делает.

| Источник | Единица | Статус |
|---|---|---|
| `alltrades quantity` (поле 12, `docs/protocol/alltrades.md`) | лоты | I (в `docs/protocol` единица не названа; по спецификации TXmlConnector «количество в лотах») |
| уровни стакана `quotes buy/sell` (`alltrades.md` §стакан, `native/records`) | лоты | I |
| `voltoday` (`<quotations>`) → `Quote.Volume` / `volumeLots` | лоты | I |
| объём свечи `volume` (`<candles>`) → `volumeLots` | лоты | I |

Живая сверка на демо-сервере (S) не проводилась; `terminal/feed/live.go` копирует `Buy`/`Sell`/`Quantity`/`VolToday`/`Volume` без пересчёта. Если S покажет акции, gateway должен делить на `LotSize` (одно место в `gateway.go`).

## GET /api/gw/v1/quotes?symbols=TQBR:SBER,TQBR:GAZP (I)

```json
{"quotes":{"TQBR:SBER":{"last":"101.00","bid":"100.90","ask":"101.10",
 "prevClose":"100.00","changePct":"1.00","volumeLots":12345,"time":1730823840000,"source":"live|seed"}}}
```

- До 200 символов, иначе `400 bad_request`; пустой `symbols` — `400`.
- Неизвестные символы **пропускаются**, не ошибка.
- `prevClose` берётся из инструмента, цены форматируются `decimals` инструмента; `changePct` — 2 знака.
- `bid`, `ask`, `prevClose` — пустая строка `""`, если значения нет (нет стакана, сессия закрыта, нет seed-значения); пустая строка = значения нет.

## GET /api/gw/v1/candles?symbol=&interval=1m&limit= (I)

```json
{"symbol":"TQBR:SBER","interval":"1m","source":"live|seed","warnings":[],
 "bars":[{"openTime":1730823840000,"closeTime":1730823899999,"open":"..","high":"..","low":"..","close":"..","volumeLots":10}]}
```

- `interval`: `1m|5m|15m|1h|1d`, иначе `400 bad_request`; неизвестный символ — `404 not_found`.
- `closeTime = openTime + periodSec*1000 - 1`; `limit` по умолчанию 500 (`0` или отсутствие — значение по умолчанию); фид ограничивает его сверху 5000 (`maxBars`).
- `source` — из истории фида; `warnings` — предупреждения фида (`[]` если чисто).

## GET /api/gw/v1/orderbook?symbol= (I)

```json
{"symbol":"TQBR:SBER","time":1730823840000,"source":"live|seed",
 "bids":[{"price":"306.25","lots":10}],"asks":[{"price":"306.30","lots":7}]}
```

Bids по убыванию, asks по возрастанию (как отдаёт фид). Неизвестный символ — `404`.

## GET /api/gw/v1/trades?symbol=&limit=50 (I)

```json
{"symbol":"TQBR:SBER","trades":[{"id":"0@1730823780000","time":1730823780000,"price":"101.00","lots":1000,"side":"buy|sell|unknown"}]}
```

По возрастанию времени. `limit` по умолчанию 50 (`0` или отсутствие — значение по умолчанию); фид ограничивает его сверху 200. Фид не несёт id сделки, поэтому `id = "<индекс в ответе>@<time>"` (**I**: нестабилен между запросами). Пустая/иная сторона — `unknown`.

## GET /api/gw/v1/account (I)

Только чтение; счёт — бумажный (paper) по умолчанию. `503 unavailable`, если бумажный брокер не подключён.

```json
{"environment":"paper","currency":"RUB","cash":"1000000.00",
 "positions":[{"symbol":"TQBR:SBER","qty":10,"avgPrice":"101.10","realizedPnl":"0.00"}],
 "openOrders":[{"id":"o1","symbol":"TQBR:SBER","side":"buy","type":"limit","status":"new","qty":5,"filled":0,"price":"50.00","time":1730823840000}],
 "fills":[{"id":"t1","orderId":"o1","symbol":"TQBR:SBER","side":"buy","qty":10,"price":"101.10","fee":"0.51","time":1730823840000}]}
```

- `qty`/`filled` — в **акциях** (не лотах): кэш paper двигается на `qty*price`.
- `openOrders` — только `new`/`partial`; статусы: `partial` -> `partially_filled`, `cancelled` -> `canceled`, остальные без изменений. `side`/`type` — строки пакета paper.
- `fills` — последние 50 по возрастанию времени. Цены — `decimals` инструмента (2, если инструмент неизвестен); `cash`, `fee`, `realizedPnl` — 2 знака.
