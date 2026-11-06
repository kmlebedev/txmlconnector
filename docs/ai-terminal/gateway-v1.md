# Gateway v1 (`/api/gw/v1/*`)

**v1 frozen; breaking change ⇒ /v2.** Статус полей: **I** (вывод из кода) до живой проверки в Task 8.

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
