# PR 67 — живая приёмка gateway v1 и коннектора (2026-11-06)

Приёмка закрывает два пункта плана
[`2026-11-06-dsh-trading-transaq-connector.md`](../superpowers/plans/2026-11-06-dsh-trading-transaq-connector.md),
которые в PR были честно отмечены как невыполненные (Task 8 Step 3 — реальный хост dsh-trading;
Task 8 Step 4 — живая проверка на демо-сервере TRANSAQ).

- **Объект:** PR 67, ветка `feat/dsh-trading-connector`, head `8c36909` (merge-base `main` — `dd38d93`).
- **Окружение live:** демо-сервер TRANSAQ (`~/re/demo.env`, секреты не печатались), окно 18:10–20:20 MSK,
  `bin/terminal -mode live -addr 127.0.0.1:18295 -llm off`, собран из PR-ветки; токен API — на запуск.
- **Только чтение:** все запросы к gateway — `GET`; торговых команд не отправлялось (paper-счёт пуст,
  в SSE 0 кадров `order`/`fill`).
- **Исполнители:** Lead (live-шлюз, коннектор, raw-проба), `contract-auditor` (независимый аудит
  контракта/диффа и live-evidence), `host-acceptance` (реальный хост dsh-trading в изолированном `/tmp`).

## Итог одной строкой

**Блокеры закрыты в `fix/pr-67-blockers`; после фиксов повторная live/host-приёмка — PASS (см. §8).**

На момент первой приёмки: gateway 64/66 (2 FAIL — orderbook), коннектор против живого шлюза 42/42,
реальный хост dsh-trading с кодом PR как есть не загружает коннектор (HOST-BLOCKED; после трёх
минимальных правок — HOST-PASS, включая агентский вызов `ru_get_ticker`). Остальные эндпоинты
gateway и коннектор подтверждены живыми данными (S).**

## Что проверялось и чем

| Проверка | Инструмент | Результат |
|---|---|---|
| Офлайн-тесты PR-ветки | `go test ./terminal/api -run TestGateway -race`; `node --test dshtrading-connector-transaq/test/`; `make connector-test` | 13/13 PASS; 39/39 PASS; exit 0 (независимо, contract-auditor) |
| Gateway v1 на живом демо | `gateway-live-check.py` (66 проверок) | **64 PASS, 2 FAIL** (orderbook) |
| Коннектор против живого gateway | `connector-live.mjs` (fake Cordis ctx, 42 проверки) | **42 PASS, 0 FAIL** |
| Причина дефекта orderbook | raw-проба `native` на демо (`<quotes>` callbacks, 35 с) | первый колбэк — полный снимок (20 уровней), далее 34 инкрементальных (28 из них ≤2 уровней), `unknownRecords=0` |
| Реальный хост dsh-trading | `dsh` 0.2.2-rc.1 + `@dshtrading/*` 0.6.3 из npm, `DSH_HOME=/tmp/dsh-trading-acceptance` | с кодом PR — **HOST-BLOCKED** (падает загрузка плагина); с 3 правками — **HOST-PASS**, `ru_get_ticker SBER` → `SBER.TQBR`, `source: "seed"` |

## 1. Gateway v1 live: что подтверждено (S)

Живая сессия: `mode=live`, `connected=true`, `source=live`, `serverTime` — свежие epoch ms.

| Эндпоинт / свойство | Наблюдение |
|---|---|
| `/info` | `version=1`, `market=ru`, `provider=finam`, `trading.paper=true`, `trading.live=false`, `intervals` 1m/5m/15m/1h/1d, `maxBars=5000` |
| `/instruments` | 11 инструментов; `decimals` 1/2/3 (LKOH 1, VTBR 3, SBER 2), `lotSize` SBER=1 / GAZP=10 (pit демо), `minStep` — ровно `decimals` знаков, `currency` RUB/USD (OZON) |
| `/quotes` | батч и wide-scan 11 символов; цены — строки ровно `decimals`; `changePct` сверен с `last`/`prevClose`; `volumeLots` int; `source=live`; неизвестные символы пропускаются; пустой `symbols` и >200 → 400 |
| `/candles` | все 5 интервалов, 955 баров: `closeTime=openTime+periodSec*1000−1` (0 расхождений), OHLC-инварианты, `limit` default 500 / cap 5000, неизвестный интервал → 400, символ → 404, `warnings=[]` |
| `/trades` | 200 сделок: цены по `decimals`, `lots` int, время по возрастанию, `id="<i>@<time>"`, `side` из допустимого словаря; default 50 / cap 200 |
| `/account` | `environment=paper`, `currency=RUB`, `cash="1000000.00"`, массивы `positions/openOrders/fills` (live-счёт пуст) |
| Auth/ошибки | без токена → 401 `unauthorized`; конверт `{"error":{code,message}}`; POST отклоняется (GET-only) |
| Единицы объёма (§ gateway-v1) | **все 4 строки — S**: GAZP (`lotSize=10`) — 102/130 значений ленты не кратны 10 (14, 5, 498, 224, 2…), уровни 823/302/1315/389/837/372, `volToday=4268901` (%10=1), свечи 1822/366/4616/445 ⇒ это не акции, а лоты. Пример «SBER — лот 10» из плана неверен для демо: pit SBER=1 |

## 2. Живые дефекты orderbook (P1)

**(a) `bids` идут по возрастанию — нарушение замороженного контракта, в scope PR.**
`terminal/api/gateway.go:278-284` (`gatewayLevels`) копирует порядок фида без сортировки;
контракт [`gateway-v1.md`](gateway-v1.md) требует bids по убыванию. Live: из 45 опросов 14 имели
≥2 bids, и **все 14** — по возрастанию (293.14 → 293.26); asks — 45/45 по возрастанию.
Фикс: сортировать bids desc / asks asc в gateway + тест с многоуровневым стаканом
(сейчас `gateway_test.go:254-275` проверяет один уровень и порядок не фиксирует).

**(b) Стакан «схлопывается» — pre-existing баг `terminal/feed`, не в диффе PR 67.**
`terminal/feed/live.go:420-431` (`applyQuotes`) заменяет книгу каждым `<quotes>`-событием;
`live.go:437-452` (`orderBookFromQuotes`) считает событие полным снимком и пропускает удаления.
Raw-проба на демо: первый колбэк — полный снимок (20 уровней), затем 34 колбэка-дельты
(28 из них ≤2 уровней, 23 % двусторонних). На gateway это видно как 37/45 односторонних опросов
(0 bids + 1 ask, 2 bids + 0 asks и т.п.). Протокол R_QUOTES инкрементален
(`native/records/quote.go:17-20,96-118`; `docs/protocol/alltrades.md:203` — «инкрементальные
обновления не проверены»). Вывод: нужен отдельный фикс `terminal/feed` (upsert qty>0, удаление
qty<0, затем сортировка) и повторная live-проверка; в PR 67 это «молча» не чинится, но
`/orderbook` и `getOrderbook` коннектора **нельзя флипать в S**.

## 3. Коннектор против живого gateway (42/42 PASS)

Подтверждено живьём: регистрация `ru`/`finam` в обоих реестрах и 6 инструментов `ru_*` (нет
`ru_place_order`/`ru_cancel_order`); канонический `SECCODE.BOARD` (`SBER.TQBR`, `GAZP.TQBR`,
`TQBR:SBER`/`sber.tqbr` → канон); `source=live`; `volume/amount = lots*lotSize` (GAZP: 4268901 → 42689010,
ровно ×10); `getKlines` 1h ×5 баров, `2h` → `TRADING_UNSUPPORTED_INTERVAL` без сети;
`getRecentTrades`/`getOrderbook` — конверсия и passthrough; `getBalances` RUB 1 000 000, пустые
позиции/заявки/fills; dry-run `placeOrder` (`dry-…`, off-step и не-кратность лоту →
`TRADING_EXCHANGE_ERROR`, `dryRun:false` → `TRADING_LIVE_TRADING_DISABLED`); `AUTH_FAILED` без токена
в тексте ошибки; `SYMBOL_NOT_FOUND`; `subscribeTicker` получает живые тики и останавливается по
`dispose`; `ru_*` tools выполняются против живого шлюза.

## 4. Приёмка в реальном хосте dsh-trading (HOST-BLOCKED → HOST-PASS с 3 правками)

Хост: `dsh` 0.2.2-rc.1 (`dsh-tools` 0.2.2-rc.1), node v26.12.0, pnpm 11.28.5,
`@dshtrading/base|gui|bot-api|router` 0.6.3 из npm, апстрим `3e98888`; `DSH_HOME=/tmp/dsh-trading-acceptance`
(изолированно, `~/.dsh` и `~/.dsh-trading` не трогались). Gateway — `bin/terminal -mode offline`
(PR `8c36909`, `source=seed`).

С кодом PR как есть хост **не загружает коннектор**:
1. `dshtrading-connector-transaq/index.js:12` (`inject = []`) и `:54` (прямое `ctx.tools?.register`) →
   `Error: cannot get property "tools" without inject`. Фикс: `inject = ['tools']`.
2. `tools.js` — у всех 6 `ru_*` нет `output { schema, render }`; `dsh-tools` 0.2.x требует его →
   `TypeError: tool "ru_get_ticker" must declare output ...`. Фикс: добавить `output`.
3. Тихая несовместимость: `index.js:44-52` читает реестры в `apply()` слишком рано — в реальном хосте
   `tradingMarketDataRegistry`/`tradingTradeRegistry` ещё `MISSING`, `ru/finam` молча не регистрируется.
   Фикс: ждать сервисы через `ctx.inject([key], …)` (как в апстрим `connector-template`).

С тремя правками: хост стартует, `ru/finam` зарегистрирован в обоих реестрах, 6 инструментов
зарегистрированы; настоящий агентский цикл (headless-профиль, модель — mock OpenAI-compatible из-за
отсутствия ключей в изолированном профиле) выполнил
`tool_call ru_get_ticker {"symbol":"SBER"}` → `symbol: SBER.TQBR, price: 293.22, source: "seed"`,
`final: completed`, exit 0.

**Ограничение апстрима (вне scope PR):** HTTP-мост `GET /dshtrading/api/tickers?market=ru` →
`400 {"code":"TRADING_PROTOCOL","message":"unknown market \"ru\""}`, `/dshtrading/api/markets` → `{"markets":[]}`
(закрытый `MARKET_IDS`: crypto/us/cn/hk/futures/global). Вкладки/моста `ru` в GUI не будет без правки
апстрима; агентские `ru_*` инструменты мост не используют.

## 5. Остальные замечания (полный список — в аудите)

- **P2:** live `lotSize`/`minStep` берутся из seed, а не из живого каталога (`live.go:251,278-300`,
  `gateway.go:135-136`) — на демо совпало, на проде возможен устаревший лот; известный символ без
  quote молча отсутствует → коннектор бросает `SYMBOL_NOT_FOUND`; `last` отсутствует → `"0.00"` вместо
  пустого значения; tools обещают «price in RUB», но в universe есть OZON (USD); PR шире плана
  (12 из 25 коммитов — T21/workflowEngine).
- **P3:** недостижимый `cancelled→canceled`; POST → 404 (не 405); `limit=-1` → 400; `quotes.time` —
  время приёма; `listInstruments` теряет currency; `config.js` принимает path; `toOrder` ставит
  `dryRun:false` бумажным заявкам; тест-гэпы (нет live-mode `source=live`, нет многоуровневого стакана).

## 6. Что осталось непроверенным

- Live: пустые `bid/ask/prevClose=""`, нулевое время, непустые `warnings`, `side=buy/sell` в ленте
  (демо отдаёт только `unknown`), непустой paper-счёт (qty в акциях/статусы/fills-50/503),
  `TRADING_NETWORK/UPSTREAM_ERROR` и recovery коннектора (шлюз не падал).
- Реальная LLM в хосте (использован mock), вызовы остальных 5 `ru_*` (зарегистрированы, не вызывались),
  вкладка/HTTP-мост `ru` (невозможны без апстрима).
- Прод-сервер TRANSAQ (проверка была на демо), полный дневной soak.

## 7. Вердикт на момент приёмки (до фиксов) и следующий шаг

**CHANGES.** Блокеры merge: P1 сортировка `bids` в gateway (+тест); P1 загрузка коннектора в реальном
хосте (`inject`/`output`/ожидание реестров); P1-external — отдельная карточка на `terminal/feed`
(инкрементальный стакан), без неё `/orderbook` остаётся **I**. Всё остальное из gateway v1 и коннектора
можно переводить в **S** по матрице аудита; README/PROGRESS обновлены этим же коммитом.

Evidence (локально, gitignored): `.re-run/acceptance-pr67-live/` (`gateway-summary.json`,
`connector-summary.json`, `raw/`, `orderbook-polls.log`, `sse-events.txt`, `probe-quotes-raw.txt`,
`audit/final-report.md`), `.re-run/host-acceptance/REPORT.md` + патчи `fix-index-js.patch`,
`fix-tools-js.patch`.

## 8. Закрытие блокеров (2026-11-06, ветка `fix/pr-67-blockers`)

Все три блокера закрыты и повторно проверены; работа велась в worktree PR 67 (от `acceptance/pr-67-live`),
новые коммиты идут после docs-коммита приёмки `5ecbf14`.

| Блокер | Фикс | Повторная проверка |
|---|---|---|
| P1-1 `bids` не по убыванию | `terminal/api/gateway.go`: `gatewayLevels(levels, decimals, descending)` копирует вход и stable-sort'ит bids desc / asks asc; `TestGatewayOrderBookSortsLevels` (многоуровневый неотсортированный fake-фид) | live 66/66; post-fix прогоны — 20/20 и 10/10 опросов двусторонние, bids desc / asks asc (до фикса было 14/14 многоуровневых сэмплов bids по возрастанию) |
| P1-3 (external) инкрементальный стакан | `terminal/feed/live.go`: persistent-книга на символ, `applyQuotes` применяет инкрементальные **upsert**-дельты (`Buy>0`/`Sell>0`), `resetBooks()` на новой сессии, сортировка сторон, `maxBookDepth=4·maxBookLevels`, deep copy на publish/read; удаление (`qty=-1`) намеренно не применяется — закреплённое кодирование даёт цену <1 (см. ограничения ниже), тесты `TestLiveOrderBookAppliesIncrementalQuotes`, `...ResetOnNewSession`, `...OutputDepth` | live: 20/20 опросов двусторонние (10 bid / 10 ask), bids desc, asks asc; книга больше не схлопывается |
| P1-2 загрузка в реальном хосте | `dshtrading-connector-transaq/index.js`: сервисы (`tools`, оба реестра) опциональны и дожидаются через `ctx.inject(...)` (fallback без `ctx.inject`); `tools.js`: у 6 `ru_*` объявлен `output {schema, render}`; тесты 42/42 | **HOST-PASS** в реальном dsh-trading 0.6.3 без `/tmp`-патчей (финальный повтор на optional `tools`): плагин активируется, `ru/finam` в обоих реестрах, 6/6 tools реально появляются в tools-сервисе через `ctx.inject(['tools'])`, агентский `ru_get_ticker SBER` → `SBER.TQBR`, `source: "seed"`, exit 0 (`.re-run/host-acceptance/recheck2/REPORT.md`) |

Повторные live-прогоны на итоговом дереве (2026-11-06 20:29–20:40 MSK, демо-сервер,
`bin/terminal -mode live` из `fix/pr-67-blockers`, только GET): **gateway 66/66 PASS**,
**коннектор 42/42 PASS** (два независимых прогона), raw-книга `TQBR:SBER` — 10/10 уровней в
20/20 и в 10/10 опросах. Статусы S/I пересобраны по этим прогонам; `getOrderbook` коннектора
подтверждён против исправленного шлюза.

Известные ограничения, оставшиеся после фиксов (не блокеры, P2/P3): удаление уровня `qty=-1` живьём не
наблюдалось, а закреплённое кодирование удаления кладёт в `model.Quote.Price` накопленную цену <1
(`native/records/quote.go`, тесты `quote_test.go`: Price=0), поэтому `terminal/feed` удаление пока
**не применяет** (no-op, уровень переживёт до новой сессии) — нужен живой захват/RE удаления; это
честно зафиксировано в `gateway-v1.md` и `docs/protocol/alltrades.md`; если сервер пришлёт поздний
полный снимок без новой сессии, уровни, отсутствующие в нём, не удалятся (на практике новая подписка
начинается со снимка, и `resetBooks()` очищает книгу); live `lotSize`/`minStep` по-прежнему из seed;
P2/P3 из §5 (agent `last`→`"0.00"`, OZON USD против «price in RUB», и т.п.) — follow-up. Внешнее
ограничение `market=ru` (HTTP-мост апстрима) не изменилось и вне scope PR.

**Итог после закрытия: блокеров merge нет; PR 67 можно принимать после обычного код-ревью и
обновления документации (сделано этим же деревом).**
