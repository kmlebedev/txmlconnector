# Gateway v1 (`/api/gw/v1/*`)

**v1 frozen; breaking change ⇒ /v2.** Живая проверка на демо-сервере TRANSAQ — 2026-11-06 (PR 67): **S** — подтверждено живьём, **I** — осталось выводом/не наблюдалось, дефекты отмечены явно. Полный отчёт — [acceptance-pr67-2026-11-06.md](acceptance-pr67-2026-11-06.md).

## Общие правила

- Только `GET`. Размещение заявок через gateway отсутствует.
- Токен: заголовок `X-Terminal-Token` (как у остального `/api/*`, кроме `/api/health`).
- Цены — десятичные строки с ровно `decimals` знаками после точки (`"0.0001"`, `"1"`).
- Время — epoch миллисекунды UTC (`0` для нулевого времени).
- Ошибки — существующий конверт `{"error":{"code","message"}}`.
- Символ gateway: `BOARD:SECCODE` (`TQBR:SBER`).

## GET /api/gw/v1/info (S)

```json
{"version":"1","market":"ru","provider":"finam","mode":"live|offline",
 "connected":true,"source":"live|seed","serverTime":1730823840000,
 "trading":{"paper":true,"live":false},
 "intervals":[{"interval":"1m","periodSec":60,"maxBars":5000}]}
```

- `source` = `seed` при `mode == offline`, иначе `live`.
- `trading.live` всегда `false`, даже при `-live-trading`.
- `intervals`: `1m`(60), `5m`(300), `15m`(900), `1h`(3600), `1d`(86400); `maxBars` — максимум свечей за запрос.

## GET /api/gw/v1/instruments (S)

```json
{"instruments":[{"symbol":"TQBR:SBER","secCode":"SBER","board":"TQBR",
 "name":"...","shortName":"...","currency":"RUB","decimals":2,
 "lotSize":10,"minStep":"0.01"}]}
```

`lotSize` — акций в лоте; `minStep` форматируется с `decimals` знаками.

## Единицы объёма: лоты (S)

Все поля `*Lots` / `lots` — лоты TRANSAQ, делений на `lotSize` gateway не делает.

| Источник | Единица | Статус |
|---|---|---|
| `alltrades quantity` (поле 12, `docs/protocol/alltrades.md`) | лоты | **S** (живой прогон 2026-11-06: GAZP, `lotSize=10` — 102/130 значений ленты не кратны 10: 14, 5, 498, 224, 2…) |
| уровни стакана `quotes buy/sell` (`alltrades.md` §стакан, `native/records`) | лоты | **S** (GAZP: 823, 302, 1315, 389, 837, 372 — не кратны 10) |
| `voltoday` (`<quotations>`) → `Quote.Volume` / `volumeLots` | лоты | **S** (GAZP: 4268901, %10 = 1) |
| объём свечи `volume` (`<candles>`) → `volumeLots` | лоты | **S** (GAZP 1m: 1822, 366, 4616, 445; %10 ≠ 0) |

Живая сверка выполнена 2026-11-06 — **S** (метод: некратность `lotSize` на GAZP; пример плана «SBER — лот 10» для демо неверен: pit SBER=1). `terminal/feed/live.go` копирует `Buy`/`Sell`/`Quantity`/`VolToday`/`Volume` без пересчёта, деления на `LotSize` нет — верно.

## GET /api/gw/v1/quotes?symbols=TQBR:SBER,TQBR:GAZP (S)

```json
{"quotes":{"TQBR:SBER":{"last":"101.00","bid":"100.90","ask":"101.10",
 "prevClose":"100.00","changePct":"1.00","volumeLots":12345,"time":1730823840000,"source":"live|seed"}}}
```

- До 200 символов, иначе `400 bad_request`; пустой `symbols` — `400`.
- Неизвестные символы **пропускаются**, не ошибка.
- `prevClose` берётся из инструмента, цены форматируются `decimals` инструмента; `changePct` — 2 знака.
- `bid`, `ask`, `prevClose` — пустая строка `""`, если значения нет (нет стакана, сессия закрыта, нет seed-значения); пустая строка = значения нет.

## GET /api/gw/v1/candles?symbol=&interval=1m&limit= (S)

```json
{"symbol":"TQBR:SBER","interval":"1m","source":"live|seed","warnings":[],
 "bars":[{"openTime":1730823840000,"closeTime":1730823899999,"open":"..","high":"..","low":"..","close":"..","volumeLots":10}]}
```

- `interval`: `1m|5m|15m|1h|1d`, иначе `400 bad_request`; неизвестный символ — `404 not_found`.
- `closeTime = openTime + periodSec*1000 - 1`; `limit` по умолчанию 500 (`0` или отсутствие — значение по умолчанию); фид ограничивает его сверху 5000 (`maxBars`).
- `source` — из истории фида; `warnings` — предупреждения фида (`[]` если чисто).

## GET /api/gw/v1/orderbook?symbol= (S)

```json
{"symbol":"TQBR:SBER","time":1730823840000,"source":"live|seed",
 "bids":[{"price":"306.25","lots":10}],"asks":[{"price":"306.30","lots":7}]}
```

Неизвестный символ — `404`; поля/единицы/`source` — **S** (live 2026-11-06).

**Исправлено и подтверждено live 2026-11-06 (ветка `fix/pr-67-blockers`):** (1) `gatewayLevels` защитно сортирует bids desc / asks asc; (2) `terminal/feed` применяет инкрементальные `<quotes>` как upsert-дельты (qty>0) в persistent-книге и сбрасывает книгу на новой сессии, поэтому стакан не схлопывается. Удаление уровня (qty=-1) пока **I**: закреплённое кодирование удаления даёт цену <1, уровень не локализуется — нужен живой захват/RE; в live-пробе 2026-11-06 удалений не было. Повторная live-проверка: 20/20 опросов двусторонние (10 bid / 10 ask), bids по убыванию, asks по возрастанию; gateway 66/66, коннектор 42/42. Доказательства — [acceptance-pr67-2026-11-06.md](acceptance-pr67-2026-11-06.md) §8.

## GET /api/gw/v1/trades?symbol=&limit=50 (S)

```json
{"symbol":"TQBR:SBER","trades":[{"id":"0@1730823780000","time":1730823780000,"price":"101.00","lots":1000,"side":"buy|sell|unknown"}]}
```

По возрастанию времени. `limit` по умолчанию 50 (`0` или отсутствие — значение по умолчанию); фид ограничивает его сверху 200. Фид не несёт id сделки, поэтому `id = "<индекс в ответе>@<time>"` (**I**: нестабилен между запросами). Пустая/иная сторона — `unknown`.

## GET /api/gw/v1/account (S — поля; live-счёт пуст)

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

## Живая приёмка 2026-11-06 (PR 67)

Демо-сервер TRANSAQ, `bin/terminal -mode live` из PR 67 (`8c36909`), только GET:

- **Подтверждено (S):** `/info`, `/instruments`, `/quotes`, `/candles` (все 5 интервалов,
  `closeTime=openTime+periodSec*1000−1`, 955 баров), `/trades` (200 сделок, asc, id-формат),
  `/account` (paper/RUB/cash/массивы; live-счёт пуст), единицы «лоты», auth/400/404/401, лимиты.
- **`/orderbook`:** изначально найденные live-дефекты (bids по возрастанию; односторонний стакан из-за
  инкрементальных `<quotes>`) исправлены в `fix/pr-67-blockers`; повторный live — двусторонний
  стакан 20/20 опросов, порядок сторон верный (см. §8 отчёта). `getOrderbook` коннектора
  подтверждён против исправленного шлюза.
- **Реальный хост dsh-trading 0.6.3:** после фиксов коннектора (`inject`, `output`, ожидание
  реестров) — HOST-PASS: плагин грузится, `ru/finam` в обоих реестрах, 6 инструментов, агентский
  вызов `ru_get_ticker` вернул данные шлюза.
- Осталось **I**: пустые `bid/ask/prevClose=""`, `0` для нулевого времени, непустые `warnings`,
  `side=buy/sell` в ленте (демо отдаёт только `unknown`), непустой paper-счёт (статусы/fills-50/503),
  `trading.live=false` при `-live-trading` (unit), `TRADING_NETWORK/UPSTREAM_ERROR`.
- Отчёт и матрица статусов: [acceptance-pr67-2026-11-06.md](acceptance-pr67-2026-11-06.md).
