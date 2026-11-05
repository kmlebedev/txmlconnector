# TRANSAQ: XML-команды нативного коннектора

Дата: 4 ноября 2026. Источник: `txmlconnector64-6.47.2.26.4.dll`, демо-сервер Finam. Статусы: **S** — сверено с DLL на демо-сервере, **D** — прочитано в декомпиляторе, **I** — вывод.

Реализация: [`native.Connector`](../../native/connector.go) — интерфейс [`connector.Connector`](../../connector/connector.go), как у DLL: XML-команда на входе, `<result>` на выходе, остальные сообщения — в callback.

## Как DLL обрабатывает команды

- Экспорт `SendCommand` (`0x179ec0`) вызывает диспетчер `FUN_1800a5030`. Он ищет команду по `id` в таблице, которую заполняет `FUN_1801774b0` (43 D-имени; нативный `dllCommands` — **45** имён: T28 добавил `newreport4` и `cancelreport4`, RPS/REPO payloads в [rps.md](rps.md)), — **D**.
- Неизвестный `id` даёт `<result success="false"><message>Unknown command.</message></result>` — **D**, **S**.
- У каждой команды есть флаг «нужно соединение». Без соединения ответ — `Cannot process this command without connection.` — **D**, **S**. «Соединение» означает завершённую загрузку: во время логина и снапшота DLL отвечает так же — **S**.
- Обработчик возвращает признак успеха и необязательные атрибуты. Успех — `<result success="true"%1%/>`, ошибка — `<result success="false"><message>…</message></result>` — **D**.
- Сетевых запросов `server_status` не делает: обработчик (`FUN_180197de0` → `FUN_1800f0f80`) отправляет рабочему потоку сообщение `0x432` и сразу возвращает успех. Поток в своём цикле (`FUN_1800f49d0`, между опросами RQ_MAIN) формирует колбэк `<server_status>` (`FUN_1800f5370`) — **D**, **S**.

## Поведение команд (эталон DLL на демо-сервере)

| Ситуация | Ответ | Колбэк |
|---|---|---|
| `server_status` без соединения / во время логина | `Cannot process this command without connection.` | — |
| `server_status` после подключения | `<result success="true"/>` | `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="0" connected="true"/>` (асинхронно) |
| `connect` | `<result success="true"/>` сразу | по окончании загрузки — `server_status connected="true"`; при ошибке — `<server_status connected="error">Ошибка подключения</server_status>` |
| `connect` при активном соединении | `Соединение уже установлено...` | — |
| `disconnect` | `<result success="true"/>` | `server_status connected="false"` **до** возврата |
| `disconnect` без соединения | `Соединение не установлено...` | — |
| `get_connector_version` | `<result success="true"/>` | `<connector_version>…</connector_version>` |
| `get_server_id` | `<result success="true"/>` | `<current_server id="0"/>` — SID из RQ_PWD, без сети |
| `get_servtime_difference` | `<result success="true" diff="-7200"/>` — DLL делает RQ_TIME | — |
| `get_markets` | `<result success="true"/>` | `<markets>…</markets>` из кэша RQ_STARTUP, асинхронно |
| `subscribe`/`unsubscribe` с `<alltrades>` (`secid` или `security`) | `<result success="true"/>` | `<alltrades>` при поступлении сделок, см. [alltrades.md](alltrades.md) |
| `get_securities` | `<result success="true"/>` | `<securities>` со всем каталогом, см. [dictionaries.md](dictionaries.md) |
| `get_securities_info` | `<result success="true"/>` | по колбэку `<sec_info secid="…">` на каждую бумагу; **без запроса к серверу** — рендер из кэша каталога и RQ_SEC_SPEC, см. раздел ниже |
| `gethistorydata` | `<result success="true"/>` | RQ_HISTORY_EXT (148) и запись 0x09 в RQ_MAIN → колбэк `<candles>`; см. [history.md](history.md) |
| `subscribe_ticks` | `<result success="true"/>` | RQ_SUBSCRIBE_TICKS (120); тики на демо вечером не пришли (S-негатив), см. [history.md](history.md) |

Атрибуты `server_status` в порядке DLL: `sys_ver` (SYS# из ответа RQ_PWD), `build` (BLD#), `server_tz`, `id` (SID#), `connected`.

`server_tz` и `sec_tz` инструментов DLL получает из 16-байтового блока часового пояса: из ответа RQ_TIME и из хвоста R_SEC_NEW. Блок похож на `TIME_ZONE_INFORMATION` в 16-битных полях. На демо-сервере встречены два блока, оба сверены с DLL на 17 176 инструментах: нулевой — `UTC`, `4cff 0000 c4ff …` (смещение −180, летнее −60) — `Russian Standard Time`. Для незнакомого блока нативный коннектор оставляет имя пустым.

## Служебные команды: `get_server_id`, `get_servtime_difference`, `get_markets`

Живой прогон: `<connect>` с параметрами по умолчанию, затем три команды на готовом соединении
(демо-сервер, 2026-11-04; сырьё — `.re-run/r6/default/`, фикстура —
`native/testdata/service-dll.xml`). "Ходит ли в сеть" проверено и по декомпилятору, и по захвату:
в кадровом потоке нет ни одного кадра в момент команд `get_server_id`/`get_markets`, для
`get_servtime_difference` виден RQ_TIME.

### `get_server_id` — SID из кэша, без сети

- Обработчик `FUN_180197ea0` → `FUN_1800f0080`: собирает `<current_server id="` + байт
  `connector+0x10` + `"/>` и кладёт строку в очередь колбэков (`FUN_1800dba90` →
  `FUN_1800dba40` = `FUN_180159090(conn+0x400, xml)` + `SetEvent`), возвращает успех — **D**.
- `connector+0x10` — тот же SID, что `id=` у `<server_status>` (`FUN_1800ffcf0`), то есть байт из
  ответа RQ_PWD (`SID#` в журнале) — **D**.
- Ответ: `<result success="true"/>` без атрибутов + колбэк `<current_server id="0"/>` (SID=0 на
  демо-сервере) — **S**. Форма элемента — `<current_server>`, а не `<servers>`/`<server_id>` из
  документации: эталон — DLL.

### `get_servtime_difference` — RQ_TIME и атрибут `diff`

- Обработчик `FUN_180197d20` → `FUN_1800f1000`: под локом вызывает `FUN_1800d9e50` →
  `FUN_180384e20`, которая **отправляет RQ_TIME** (builder `FUN_1803b7ca0`, тип 50), берёт локальное
  время и считает `(server − local)` в **десятых секунды** с поправкой на RQL:
  `(srv−local)*10 + RQL_ms/200`, округление ±0.5; sample пишется кольцом через `FUN_18026d7d0`,
  `FUN_180255a30` считает взвешенное среднее (веса 3/2/1) и делит на 10 → **секунды** — **D**.
- Результат: атрибут `diff` у `<result>`: `<result success="true" diff="-7200"/>`; отдельного
  колбэка нет — **S**. В `*_ts.log` при этом печатается `Server time:… RQL:26, Diff*10:-72000`, что
  согласуется с формулой — **D**/**S**.
- Единицы и знак: `diff = server wall time − local wall time` в секундах (на машине RE-2 local был
  UTC+5, сервер МСК → −7200) — **S**.
- В захвате при команде виден RQ_TIME (`C>S` type=50 и ответ) — **S**; это единственная из трёх
  команд, которая ходит в сеть.

### `get_markets` — кэш RQ_STARTUP через рабочий поток

- Обработчик `FUN_180197ca0` → `FUN_1800f0f40`: `FUN_1800f4ca0(conn, 0x437, 0, 0)` =
  `PostThreadMessage(0x437)` + `SetEvent`, сразу возвращает успех — **D**.
- Сообщение `0x437` обрабатывает рабочий поток `FUN_1800f49d0` (`CMP … , 0x437` @
  `0x1800f4a45`) и рендерит `<markets>` из кэша RQ_STARTUP; сетевых запросов нет — **D**.
- Ответ: `<result success="true"/>` сразу, колбэк `<markets>…</markets>` асинхронно (в живой сессии
  через ~25 мс) — **S**. Язык имён (`ММВБ`/`MICEX`) задаётся параметром `<connect>` `language`
  (см. [connect.md](connect.md)) — **S**.

Фикстура `native/testdata/service-dll.xml` содержит эталонный вывод трёх команд (account-independent
строки; `diff` зависит от часового пояса машины — в фикстуре −7200 для local UTC+5 и МСК).

## `get_securities_info` → `<sec_info>` (S/D)

Запрос — одна из форм (обе проверены живьём, r11 2026-11-04):

```xml
<command id="get_securities_info">
  <security><market>1</market><seccode>SBER</seccode></security>
  <security><market>4</market><seccode>SiZ6</seccode></security>
</command>
<command id="get_securities_info"><secid>26</secid><secid>20583</secid></command>
```

Обработчик `FUN_180197e60` → `FUN_1800f02f0`: разбирает элементы `security` (`seccode`+`market`)
или `secid`, ищет бумагу в **локальном каталоге** (`FUN_180252260`/`FUN_1802525b0`) и рендерит
`<sec_info secid="…">` функцией `FUN_180105990`; колбэки отправляются через `FUN_1800dba90`.
Никакого запроса к серверу команда не делает — **S**: в живом захвате между командой и колбэками нет
ни одного C>S-кадра, данные берутся из кэша RQ_STARTUP/R_SEC_NEW и spec-кэша, который DLL получает
ответом на RQ_SEC_SPEC (15) после подписки (`docs/protocol/dictionaries.md`, раздел RQ_SEC_SPEC).
Если бумага не найдена — DLL отдаёт текстовое сообщение об ошибке (`Failed to get symbol
seccode(…), market(…)` / `Cannot find security (secid=…)`); если нет spec-кэша — исключение
`GetSecSpec returned null` (живьём не воспроизводили, **I**).

Состав `<sec_info>` (порядок элементов, D): `secname, seccode, market, pname, mat_date,
clearing_price, minprice, maxprice, (bgo_c, bgo_nc, bgo_buy | buy_deposit, sell_deposit),
coupon_value, coupon_date, coupon_period, facevalue, put_call, opt_type, accruedint, point_cost,
lot_volume, isin, regnumber, buybackprice, buybackdate, currencyid`; пустые/нулевые поля
пропускаются. Живые примеры и байты spec-ответов — `native/testdata/secinfo-live.hex.gz`,
`secinfo-dll.xml.gz`, описание — `native/testdata/secinfo-live.md` (**S** для значений, **D** для
порядка).

## `gethistorydata` и `subscribe_ticks` (S/D)

Обе команды проверены живьём r14–r16 (2026-11-04); подробности, форматы запросов и фикстуры —
[history.md](history.md). Кратко:

- `gethistorydata` принимает `<security><board>…</board><seccode>…</seccode></security>` +
  элементы `period`, `count`, `reset` либо те же значения атрибутами команды (атрибутная форма
  требует уже установленного соединения). DLL шлёт **RQ_HISTORY_EXT (148)**; RQ_HISTORY (11) не
  наблюдался. Свечи приходят записью **0x09** через RQ_MAIN и рендерятся в `<candles
  secid=… board=… seccode=… period=… status=…>` с `<candle date=… open=… close=… high=… low=…
  volume=…/>`; `status=1` — заказанное выдано, `status=2` — продолжение (на демо не встречался).
  Фикстуры: `native/records/testdata/candles-live.hex.gz` + `candles-dll.xml.gz`.
- `subscribe_ticks` требует элемент `filter` и у каждого `security` — дочерний `tradeno`; payload
  RQ_SUBSCRIBE_TICKS: `B filter; {W secid, W board, G tradeno}*`. В вечерней сессии демо тиков не
  было (ни RQ_TICKS 13, ни `<ticks>`) — **S-негатив**; для сверки нужен прогон в основную сессию.


## `set_csp` и `authenticate` (T29, D + O)

Обе команды **обрабатываются DLL и без соединения, и с соединением** — в матрице 45 команд они не
«not implemented»; после `connect` они отвечают своими предметными ошибками (O, replay loopback
2026-11-05, `.re-run/t49/auth1`). Сетевых запросов не делают.

### `set_csp` — D: `FUN_180197d60` → `FUN_180109170`

- Вход: `<command id="set_csp"><csp type="…"/></command>`; элемент `csp` и его атрибут `type`
  (D: `FUN_18040c140`/`FUN_18040cf20`). Допустимые значения `type` (D, строки
  `0x180835a90`/`0x180835a44`…): `NONE` (0), `MS` (1), `CP` (2), `SC` (3), `MPSSL`? (`MP` 4,
  `MPSSL` 5), `CPBB` (6), `CPBB_2012_256` (7), `CPBB_2012_512` (8); тип сохраняется в
  `connector+0x688`.
- Нет элемента `csp` → **успех** (no-op, D: ветка `local_d0 == 0 → return 1`; O: пустая команда
  `<command id="set_csp"/>` → `<result success="true"/>`).
- Элемент есть, атрибута `type` нет → `You have to specify at least one CSP.` (D).
- Неизвестный `type` → `Unsupported CSP type.` (D).
- **Соединение установлено** → `Данная операция требует отключения от сервера. Необходимо выполнить
  команду Disconnect.` (D: CP1251-строка `0x180835aa0`; O: то же после `connect` — все варианты
  `set_csp` вернули эту ошибку).

### `authenticate` — D: `FUN_180197da0` → `FUN_1800d8850`

- Поведение зависит от криптопровайдера (`connector+0x678`, режим `+0x10`): при режиме 3 требуется
  атрибут `pin` (D: ошибка `attribute "pin" not found`), дальше — асинхронная аутентификация с
  колбэком `<authentication status="error">Authentication request failed</authentication>` (D) или
  успешным колбэком (форма не снята — **I**).
- O (обе команды до и после `connect`, провайдер по умолчанию): `<result success="false"><message>Illegal
  context for "sendpin" command</message></result>`; атрибут `pin` на результат не влияет.
- Для матрицы: `authenticate` не «not implemented»; конкретный успешный сценарий требует
  настроенного криптопровайдера (I, вне T29).


## S-серия на проде (2026-11-05, read-only): подтверждено

| Команда | Ответ DLL (S) | Колбэк (S) |
|---|---|---|
| `get_old_news count="5"` | `<result success="true"/>` | 5 × `<news_header>` (RQ_MAIN `0x36 flags=1`) |
| `get_news_body news_id="N"` | `<result success="true"/>` | `<news_body>` (RQ_MAIN `0x36 flags=2`) |
| `subscribe_ticks` (board/seccode/tradeno) | `<result success="true"/>` | `<ticks><tick>…` (RQ_MAIN `0x5a`) |
| `get_client_limits client="<код>"` | `<result success="true"/>`; C>S `type=43` (RQ_CLIENT_LIMITS), reply `type=43` | `<clientlimits>` |
| `get_portfolio` | `<result success="true"/>` (локальный рендер) | `<portfolio_currency>` |
| `get_united_portfolio` | `<result success="true"/>` (локальный рендер) | `<united_portfolio>` |
| `get_mc_portfolio client="<код>" union="<код>"` | `<result success="true"/>` | `<mc_portfolio>` |
| `get_portfolio_mct client="<код>"` | `{8} Неверное значение параметра 'szClient'.` | — |
| `get_portfolio_tplus` / `get_portfolio_forts` | `Unknown command.` | — |
| `unsubscribe_ticks` | `Unknown command.` | — |

Прочее (S): `connect` на проде — `<result success="true"/>`, загрузка ~10–12 с (838 `sec_info_upd`,
61 `securities`, 61 `pits`, 5 `<client>`, `<union>`); `server_status` после загрузки — success и
`<server_status …>`. Ответ `get_client_limits` без `client`: `Client not specified.`.

## Нативная реализация

- `connect` разбирает `commands.Connect` (`host`, `port`, `login`, `password`, `rqdelay`, `request_timeout`) и сразу возвращает успех. Дальше в фоне идут логин, RQ_TIME, RQ_STARTUP, колбэки `<markets>`, `<boards>`, `<candlekinds>` (побайтно как у DLL), выборка снапшота RQ_MAIN с колбэками `<securities>` на каждый опрос с новыми инструментами, `<pits>`, затем `server_status connected="true"`. После этого RQ_MAIN опрашивается каждые `rqdelay` (по умолчанию 100 мс, не меньше 10 мс), как в DLL, и это держит сессию.
- Потеря связи: колбэк `server_status connected="true" recover="true"` в момент обрыва; короткое восстановление (`RQ_RELOGIN`/`RQ_RECOVER`) сохраняет сессию и каталог, иначе — `connected="false"` и новый `connect` (см. [connect.md](connect.md)).
- `disconnect` останавливает опрос, даже если запрос RQ_MAIN уже в полёте. Затем отправляет `RQ_LOGOUT` (без ответа, таймаут 4 с) и отдаёт колбэк `connected="false"` до возврата.
- `subscribe`/`unsubscribe` поддерживают `<alltrades>`; `<quotations>` и `<quotes>` отвечают «not implemented».
- `get_securities` отдаёт `<result success="true"/>` и колбэк `<securities>` со всем каталогом, элемент в элемент как DLL (`FUN_18011f4a0`, см. [dictionaries.md](dictionaries.md)).
- `get_server_id`, `get_servtime_difference` и `get_markets` реализованы по R6 (`native/testdata/service-dll.xml`): SID и рынки из кэша без сети, разница времени — через RQ_TIME, ответ с атрибутом `diff` (см. ниже).
- Остальные 33 команды из таблицы DLL (всего их 43) отвечают `Command is not implemented by the native connector.`, а если требуют соединения и его нет — сообщением DLL.
- `get_connector_version` возвращает `6.47.2.26.4-native`: версию DLL, поведение которой воспроизводится, с пометкой реализации.
- Колбэки доставляются по порядку из одной горутины, как из потока колбэков DLL.

Расхождения с DLL, известные на сегодня:
- DLL в конце загрузки перед `connected="true"` отдаёт `<client>`, `<positions>`, `<overnight status="false"/>` (живой захват 4.11) — нативный коннектор их не эмитит (торговый контур, M6); события `<sec_info_upd>` (684 за 81 с live-сессии) тоже ещё не эмитятся, см. [dictionaries.md](dictionaries.md).
- Перед каждым `<server_status>` (в конце загрузки и в ответ на команду `server_status`) DLL отдаёт `<overnight status="false"/>` — наблюдение из живого захвата 4.11, генератор не проверяли; нативный коннектор эмитит только статус.
- Тексты ошибок разбора XML и проверки параметров `connect` у DLL не сверялись.

## Проверка

```sh
go test -race ./native/
# сценарий команд на демо-сервере: DLL (под Wine) и Go
harness.exe '<command id="connect">…</command>' sleep:8s '<command id="server_status"/>' …
go run ./tools/reference-harness -backend native '<command id="connect">…</command>' sleep:8s '<command id="server_status"/>' …
```

Тест `native/connector_test.go` поднимает поддельный TRANSAQ-сервер на тех же примитивах протокола (логин, сессионный ключ, RQ_TIME демо-сервера, RQ_STARTUP из фикстуры). Он сверяет весь сценарий из таблицы выше с ответами DLL, а также потерю связи, ошибку подключения и `disconnect` во время опроса.
