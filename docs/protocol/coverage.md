# Матрица покрытия native-клиента (Task 30) — черновик

Дата: 2026-11-05. Ветка: `plan/native-client`. Источники: фактическое состояние кода,
`docs/superpowers/plans/2026-11-04-native-transaq-client.md`, `docs/protocol/*`,
`docs/deepseek/task-index.md`. Документ — черновик Task 30 (обновляется в конце фазы 9);
фиксирует то, что реально реализовано и проверено, и что осталось на RE/S-сессии.

Статусы фактов (как в docs/protocol):

- **S** — живой демо-сервер; **O** — оракул DLL (replay/RE-3); **D** — декомпилятор;
- **I** — вывод/не восстановлено (не считается доказанным).

Статусы реализации: **done** — реализовано и покрыто тестами; **partial** — реализована
часть (D-часть/один из видов), остальное явно I; **stub** — только интерфейсы/константы,
явные I-ошибки; **none** — не начато.

## 1. Сводка по задачам плана (1–30)

| № | Задача | Фаза | Статус | Ключевые артефакты / тесты |
|---|---|---|---|---|
| 1 | Прокси и проигрыватель | 0 | done | `tools/tcproxy`, `tools/replay-server`, `native/replay`, `native/replay/*_test.go` |
| 2 | model и records | 0 | done | `native/model`, `native/records` (+тесты) |
| 3 | Событийный Client.Run | 0 | done | `native/client.go`, реестр декодеров, `TestRun*` |
| 4 | Шаблон семейства + checkDLL | 0 | done | `xmlcompat.Render`, `native/records/oracle_test.go` |
| 5 | RQ_RELOGIN 63 / RQ_RECOVER 64 | 1 | done | `native/session/recover*.go`, `session/testdata/recover-*`, `TestRecover*` |
| 6 | Служебные команды/параметры connect | 1 | done | `native/client_service.go`, `native/session/login*`, `connect-options-live*` |
| 7 | Конец загрузки: client/positions/overnight/union | 1 | done | `native/client_accounts.go`, `snapshot-tail-live*`, `TestSnapshotTailMatchesDLL` |
| 8 | sec_info_upd из R_SEC_PARAM | 2 | done | `native/records/secparam.go`, `native/client_secinfo.go`, `secparam-live*` |
| 9 | quotations (маска 0x01, 0x06) | 2 | done | `records/quotation.go`, `quotation-live*`, `quotations-dll.xml` |
| 10 | quotes/стакан (0x02, 0x07) | 2 | done | `records/quote.go`, `quote-live*`, `quotes-dll.xml` |
| 11 | get_securities_info → sec_info | 2 | done | `native/client_secinfo.go`, `native/testdata/secinfo-*`, RQ_SEC_SPEC 15 |
| 12 | Остальные записи снапшота | 2 | done | `records/records.go` splitter (AmBOOK, SYSPARAMS, wrapper, 0x05), `TestE07TailHasNoUnknownRecords` |
| 13 | gethistorydata → candles | 3 | done | `client_history.go`, `records/candles.go`, `candles-*`; фиксы 8277d6f |
| 14 | subscribe_ticks → ticks | 3 | **partial** | `client_ticks.go`, `records/ticks.go`, `session/ticks.go`, команда `subscribe_ticks`; XML `<ticks>`/рендер — нет (ждёт S/O, T14 DoD) |
| 15 | RQ_TRANSACT 14 | 3 | done | `session/transact.go`, `transact-*`, `TestTransact*/LostReply/EmptyReply` |
| 16 | neworder/cancelorder, orders | 3 | done | `client_orders.go`, `records/order.go`, `order-*`; фиксы c6a3143 |
| 17 | trades (R_TRD 0x04) | 3 | done | `records/clienttrade.go`, `trade-*`; фиксы 177879c |
| 18 | newcondorder/newstoporder/cancelstoporder, R_STOP | 3 | done | `client_stoporders.go`, `records/stoporder.go`, `stoporder-*`, `transact-*-payload.hex`; фикс 64e7f8b |
| 19 | moveorder/makeordown | 4 | **none** (blocked) | нет payload без живой активной заявки; задача 44 RE; connector-ветки нет |
| 20 | positions: деньги/бумаги/FORTS | 4 | done | `client_positions.go`, `records/positions.go`, `position-*` |
| 21 | get_forts_positions/get_client_limits | 4 | **partial** | `client_positions.go`, `records/limits.go`, `session/limits.go`; oracle `<clientlimits>` ждёт; RQ_FORTS_FIRM_MONEY 47 не реализован (формат I) |
| 22 | portfolio-команды | 4 | **partial** | `client_portfolio.go`, `records/portfolio.go`, `xmlcompat` local render; O-фикстуры `portfolio-currency/mc-dll.xml`; остальные формы/типы — I (5f3ef49, 5bd73dc) |
| 23 | max buy/sell, cln_sec_permissions, leverage | 5 | **stub** | каркас 6a57f75: `model/margin.go`, `client_margin.go`, I-ошибки; ждёт RE-2 |
| 24 | news/get_old_news/get_news_body | 6 | **stub** | каркас 6a57f75: `model/news.go`, `records/news.go`, `session/news.go`; ждёт RE-2 |
| 25 | messages (R_MSG 0x01) | 6 | **stub** | каркас 6a57f75: `model/message.go`, `records/message.go`; layout I |
| 26 | change_pass | 6 | **none** | только RE-3 без живой смены пароля |
| 27 | options | 7 | **none** | — |
| 28 | RPS/REPO/отчёты | 8 | **none** | — |
| 29 | XML-коннектор целиком, native backend | 9 | **partial** | `native/connector.go`: 26/43 команд; `server/` native backend нет; матрица 45 имён — в этом документе `2 |
| 30 | Матрица покрытия, soak, выпуск | 9 | **in progress** | этот документ; soak/выпуск — после 29 |

Фазы 0–2 закрыты по AGENTS.md (коммиты в истории ветки, PR #47/#48 — в main).

## 2. XML-команды Connector (`dllCommands`, 43 имени)

Всего в `dllCommands` 43 имени; реализовано в `SendCommand` 26. Специальные
(`wait=false`): connect, disconnect, authenticate, set_csp, get_connector_version.
Расхождение плана: нет `newreport4`/`cancelreport4` (45 vs 43, task-index `8.1).

### 2.1 Сетевые команды (шлют RQ_* в сессию)

| Команда | Реализация | Запрос | Факт | Тесты/фикстуры | Примечание |
|---|---|---|---|---|---|
| connect | done | RQ_LOGIN 1 + RQ_STARTUP 9 | S/O | `TestLoginAgainstRecordedServer`, `startup-demo.hex`, `connect-options-live*` | параметры/язык/UTC — done (task 6) |
| disconnect | done | RQ_LOGOUT 2 | S | `TestLogoutSendsWithoutWaiting` | — |
| subscribe / unsubscribe | done | RQ_SUBSCRIBE 10 | O | `subscribe*`, `TestSubscribeMasksMatchDLL` | маски 0x01/0x02 |
| subscribe_ticks | done (команда) | RQ_SUBSCRIBE_TICKS 120, опрос RQ_TICKS 13 | S/O | `TestConnectorSubscribeTicks`, `session/ticks_test.go` | XML `<ticks>` ещё нет (T14 partial) |
| gethistorydata | done | RQ_HISTORY_EXT 148 (RQ_HISTORY 11 — константа) | O | `TestConnectorHistory*`, `candles-*` | результат — до candles (не блокирующе) |
| get_forts_positions | done | RQ_CLIENT_HOLDINGS 42 | O | `TestPositionsMatchDLL`, `position-forts-*` | FORTS-позиции |
| get_client_limits | done (D) | RQ_CLIENT_LIMITS 43 | D (+oracle pending) | `TestClientLimitsCommand/Parse` | `<clientlimits>` XML — синтетика; ждёт O-фикстуру |
| get_servtime_difference | done | RQ_TIME 50 | O | `TestServerTimeDifference*` | — |
| neworder | done | RQ_TRANSACT 14 | O | `TestNewOrderPayloadMatchesDLL/PriceScale`, `transact-neworder-payload.hex` | цена rescale к decimals (c6a3143) |
| cancelorder | done | RQ_TRANSACT 14 | O | `TestCancelOrderPayloadMatchesDLL/Command` | — |
| newstoporder | done | RQ_TRANSACT 14 | O | `TestNewStopOrderPayloadMatchesDLL/Legs`, `transact-newstoporder-payload.hex` | SL-only/TP-only/both 27/26/35 Б (64e7f8b) |
| newcondorder | done | RQ_TRANSACT 14 | O | `TestNewCondOrderPayloadMatchesDLL/ConditionTypes`, `transact-newcondorder-payload.hex` | cond_type byte16 01/02/04; Last отказ |
| cancelstoporder | done | RQ_TRANSACT 14 | O | `TestCancelStopOrderPayloadMatchesDLL`, `transact-cancelstoporder-payload.hex` | payload 17 + packed L id |

### 2.2 Локальные/справочные команды (без нового RQ, кроме sec_spec)

| Команда | Реализация | Источник | Факт | Тесты/фикстуры | Примечание |
|---|---|---|---|---|---|
| server_status | done | локальный рендер overnight+status | O | `TestServerStatusLifecycleMatchesDLL` | — |
| get_connector_version | done | конфиг/константа | S | `TestServiceCommandsMatchDLL` | `wait=false` |
| get_server_id | done | кэш RQ_PWD (SID) | O | `TestServiceCommands` | — |
| get_markets | done | каталог RQ_STARTUP | O | `markets-demo.xml` | — |
| get_securities | done | каталог RQ_STARTUP (без нового запроса) | O | `securities1-demo.xml`, `TestConnectorSecuritiesOnLoad/GetSecurities` | — |
| get_securities_info | done | локальный каталог/spec cache, без запроса (S, r11); RQ_SEC_SPEC 15 — при подписке/загрузке | O | `native/testdata/secinfo-*`, `TestConnectorGetSecuritiesInfo` | — |
| get_portfolio | done | локальный рендер из сторов | O | `portfolio-currency-dll.xml`, `TestPortfolioLocalRender` | без сетевых кадров (O) |
| get_mc_portfolio | done | локальный рендер | O | `portfolio-mc-dll.xml`, `TestPortfolioLocalRender` | требует client/union; O-ошибка без них |
| get_portfolio_mct | done (ошибка) | — | O | `TestPortfolioLocalRender` | «{8} Неверное значение параметра szClient», формат входа I |
| get_united_portfolio/equity/go | stub (I) | — | I | `TestPortfolioLayoutsAreExplicitI` | «layout not recovered (I)» |

### 2.3 Не реализовано в Connector (default → message not implemented)

authenticate, set_csp (внешние криптопровайдеры; T29), cancelnegdeal, change_pass,
get_cln_sec_permissions, get_family_strikes, get_max_buy_sell, get_max_buy_sell_tplus,
get_news_body, get_old_news, get_option_families, get_options, makeordown, moveorder,
newmrepoorder, newrepoorder, newrpsorder (17 имён). Соответствие задачам: T19 (moveorder/
makeordown), T23 (max buy/sell, permissions), T24 (news), T26 (change_pass), T27 (options),
T28 (RPS/REPO). Плюс отсутствуют `newreport4`/`cancelreport4` (T28/T29).

## 3. Записи RQ_MAIN (splitter `records.Records` + декодеры `native/events*`)

| Tag | Запись | Splitter | Декодер/состояние | XML-форма | Факт | Тесты/фикстуры |
|---|---|---|---|---|---|---|
| 0x01 | R_MSG | — (нет case) | нет | `<messages>` нет (T25 stub) | D/I | — |
| 0x02 | R_CLN_NEW (sized) | sizedTags | decodeClientNew → ClientAccount + pendingOrders | `<client>` (конец загрузки) | O | `snapshot-tail-live*`, `TestSnapshotTailMatchesDLL` |
| 0x03 | R_ORD_NEW (sized) | sizedTags | decodeOrderNew → Order (фильтр client id) | `<orders>` | O | `order-new-*`, `TestConnectorOrders` |
| 0x04 | R_TRD (sized) | sizedTags | decodeClientTrade → ClientTrade | `<trades>` | O | `trade-*`, `TestConnectorTradesCallback` |
| 0x05 | state | special | state-only (пусто) | — | D | `TestStateOnlyRecords` |
| 0x06 | R_QUOTATION | splitter по маске | decodeQuotation → Quotation | `<quotations>` | O | `quotation-live*`, `quotations-dll.xml` |
| 0x07 | R_QUOTES | splitter | decodeQuotes → Quotes | `<quotes>` | O | `quote-live*`, `quotes-dll.xml` |
| 0x08 | R_ALLTRADE (sized) | sizedTags | decodeAllTrade → Trade | `<alltrades>` | O | `alltrades-*` |
| 0x09 | R_HISTORY | special (unsized) | decodeCandles → Candles | `<candles>` | O | `candles-*` |
| 0x0a | R_SEC_RATE | special (W+L) | state-only (нет события) | — | D | — |
| 0x0b–0x0e | R_SEC_ZALOG/OBLIG/ONOFF/0E | attrTags 3 Б | state-only (нет события) | — | D | `TestRecordsAttributeUpdates` |
| 0x10 | R_ORD_MODIFY (sized) | sizedTags | decodeOrderModify → Order (все mask-ветки) | `<orders>` (один финальный) | O | `order-modify-*`, `TestOrderModifyBatchOneCallback` |
| 0x18 | R_OVERNIGHT | 1 байт | decodeOvernight → Overnight | `<overnight>` | O | `TestOvernightRecordReachesStatus` |
| 0x28 | R_SEC_NEW (sized) | sizedTags | нет декодера (каталог из RQ_STARTUP) | — | D/I | `TestParseSecNewSnapshot` (records) |
| 0x34 | R_BOARD (sized) | sizedTags | нет декодера (каталог из RQ_STARTUP) | — | D/I | — |
| 0x35 | R_NEWS | — (нет case; только TagNews) | нет (T24 stub) | `<news_header>` нет | D/I | — |
| 0x36 | R_NEWS_UPLOAD | — (нет case) | нет (T24 stub) | — | D/I | — |
| 0x3a | R_ORD_RENEW (sized) | sizedTags | нет декодера | — | D/I | — |
| 0x3b | R_CLIENT_ASSET (sized) | sizedTags | decodeClientAsset → MoneyPosition/SecPosition/Forts* | `<positions>/<sec_positions>/<forts_positions>` | O | `position-*`, `snapshot-tail*` |
| 0x3d | R_DATE | special (L) | state-only | — | D | `TestParseEndOfLoadRecords` |
| 0x54 | R_STOP (sized) | sizedTags | decodeStopOrder → StopOrder (фильтры client/secid) | `<stoporders>` (mask-aware) | O | `stoporder-*`, `TestStopOrdersMatchDLL` |
| 0x5b | R_SYSPARAMS | L size + blob | state-only (blob I) | — | D/I | `sysparams-inject.hex`, `TestSizedMiscRecords` |
| 0x5c | AmBOOK | splitter по flags | state-only (нет события) | — | D | `misc-live*`, `TestMiscLiveRecords` |
| 0x5e | R_SEC_PARAM (sized) | sizedTags | decodeSecParam → SecInfoUpdate | `<sec_info_upd>` | O | `secparam-live*`, `TestSecInfoUpdMatchesDLL` |
| 0x5f | R_WRAPPER (sized) | sizedTags → nested | вложенные записи (0x6e skip) | — | O/D | `TestE07TailHasNoUnknownRecords` |
| 0x62,0x67 | MarCat/Unicat | 2 Б | state-only | — | D | `TestRecordsStopsAtUnknownLayout` (контекст) |
| 0x66 | R_DOUBLELEG (sized) | sizedTags | нет декодера | — | D/I | `TestSizedMiscRecords` |
| 0x68 | R_UNION | splitter (G+Z20+Z32+B) | decodeUnion → Union | `<union>` | O | `TestUnionRecordReachesLoadEnd` |
| 0x69 | ReadURTUpdate | splitter до 0xff | state-only | — | D | — |
| 0x6b | R_FORTS_SESSION | splitter (L) | state-only | — | D | `TestSizedMiscRecords` |
| 0x6e | R_EDP_RATE | splitter (Z4,Z25,B,L) | нет декодера (nested в 0x5f) | — | D/I | `misc_test` |
| 0x7c | ReadICA (sized) | sizedTags | нет декодера | — | D/I | `TestSizedMiscRecords` |

Поведение неизвестных записей: `records.Records` возвращает `UnknownRecordError` и уже
разобранный префикс; `Client.Run` сообщает `model.UnknownRecord` (тест
`TestRunReportsUnknownRecord`); `LastUnknownTag` фиксирует тег. На живом E07-хвосте
`UnknownRecords == 0` (`TestE07TailHasNoUnknownRecordsAfterLoad`).

## 4. Reply-кадры session (Rq_* константы и обработчики)

| RQ | id | Метод/обработчик | Разбор | Факт | Фикстуры/тесты |
|---|---|---|---|---|---|
| RQ_LOGIN | 1 | login (Connect) | login reply + startup | S/O | `TestLoginPayloadMatchesDLL`, login-* |
| RQ_LOGOUT | 2 | Logout (без ожидания) | — | S | `TestLogoutSendsWithoutWaiting` |
| RQ_MAIN | 5 | Conn.Main | `records.Records` | O | `main1-demo.hex`, `e07-tail-live*` |
| RQ_STARTUP | 9 | Conn.Startup | `records.ParseStartup` | O | `startup-demo.hex`, `TestStartupMatchesDLLCallbacks` |
| RQ_SUBSCRIBE | 10 | Conn.Subscribe | — | O | `TestSubscribePayloadMatchesDLL` |
| RQ_HISTORY | 11 | Conn.HistoryExt (RQ_HISTORY_EXT 148) | `records.ParseCandles` | O | `candles-*`, `TestConnectorHistory*` |
| RQ_TICKS | 13 | Conn.PollTicks | `records.ParseTicks` | S/O | `session/ticks_test.go` |
| RQ_TRANSACT | 14 | Conn.Transact | reply XML (success/error/empty) | O | `transact-*-reply.hex`, `transact-*-dll.xml` |
| RQ_SEC_SPEC | 15 | Conn.SecSpec | `records.ParseSecInfo` | O | `secspec-request.hex`, `native/testdata/secinfo-*` |
| RQ_CLIENT_HOLDINGS | 42 | используется positions/securities | `records.ParseClientHoldings/ParseSecNew` | O | `position-sec/forts-*` |
| RQ_CLIENT_LIMITS | 43 | Conn.ClientLimits | `records.ParseClientLimits` | D (+oracle pending) | `TestClientLimits*` |
| RQ_PWD | 46 | login/recover key | layout — `docs/protocol/connect.md` | O/D | `TestPwdPayloadLayout` |
| RQ_TIME | 50 | Conn.Time | `ServerTime` | O | `TestServerTimeDifference` |
| RQ_RELOGIN | 63 | Conn.Relogin (recover short) | `records.ParseReloginReply` | O | `session/testdata/recover-short-live*` |
| RQ_RECOVER | 64 | Conn.Restore (recover long) | `records.ParseRecoverParts` | O | `session/testdata/recover-long-live*` |
| RQ_HANDSHAKE | 106 | — (внешние криптопровайдеры) | — | D | не реализовано; вне встроенного профиля |
| RQ_SUBSCRIBE_TICKS | 120 | Conn.SubscribeTicks | payload builder | S/O | `TestTicksPayloadMatchesDLL` |
| RQ_HISTORY_EXT | 148 | Conn.HistoryExt | `records.ParseCandles` | O | `TestHistoryPayloadMatchesDLL` |
| RQ_TRNS_IN_PROGRESS | 156 | Conn.TransactionsInProgress | packed L | O | `TestTransactionsInProgress` |
| RQ_ENFORCE_TRN_RELEASE | 157 | константа (не отправляется) | — | D/I | — |
| RQ_GET_RPSREPO / MARGIN_POS(_LIST) / FOND_PORTFOLIO | 82/84/85/86 | только константы (T22 D-part) | — | D/I | `session/portfolio_test.go` |

## 5. XML-колбэки и рендер

| XML | модель/событие | Render/функция | Тест-фикстура | Факт |
|---|---|---|---|---|
| `<orders>` | Order | `OrdersXML` | `order-*` | O |
| `<stoporders>` | StopOrder | `StopOrdersXML` (mask-aware) | `stoporder-*` | O |
| `<trades>` | ClientTrade | `AllTradesXMLOptions` | `trade-*` | O |
| `<alltrades>` | Trade | `AllTradesXMLOptions` | `alltrades-*` | O |
| `<positions>` | MoneyPosition | `PositionsXML` | `snapshot-tail-live.xml` | O |
| `<sec_positions>` | SecPosition | `SecPositionXML` | `position-sec-*` | O |
| `<forts_positions>` | FortsPosition | `FortsPositionsXML` | `position-forts-*` | O |
| `<clientlimits>`/`<united_limits>` | ClientLimits/UnitedLimits | `ClientLimitsXML`/`UnitedLimitsXML` | нет O-фикстуры | D (+oracle pending) |
| `<candles>` | Candles | `CandlesXML` | `candles-*` | O |
| `<quotations>` | Quotation | `QuotationsXML` | `quotation-live*` | O |
| `<quotes>` | Quotes | `QuotesXML` | `quote-live*` | O |
| `<sec_info_upd>` | SecInfoUpdate | `SecInfoUpdateXML` | `secparam-live*` | O |
| `<sec_info>` | SecInfo | `SecInfoXML` | `native/testdata/secinfo-*` | O |
| `<startup>`/`<markets>`/`<securities>` | Catalog | `StartupXML`/`MarketsXML`/`SecuritiesDumpXML` | demo XML | O |
| `<client>`/`<overnight>`/`<union>` | ClientAccount/Overnight/Union | `ClientXML`/`OvernightXML`/`UnionXML` | `snapshot-tail-live.xml` | O |
| `<server_status>`/`<connector_version>`/`<current_server>` | ServerStatus/константы | `StatusXML`, строки connector | `service-dll.xml` | O/S |
| `<portfolio_currency>`/`<mc_portfolio>` | MoneyPosition (local) | `PortfolioCurrencyXML`/`MCPortfolioXML` | `portfolio-*-dll.xml` | O |
| `<portfolio_tplus>`/`<portfolio_forts>`/`<united_portfolio>`/`<portfolio_mct>` | — | только литеральные скелеты, не в Render | — | I (T22 remaining) |
| `<ticks>` | Tick | рендер не реализован | нет | T14 partial |
| `<news_header>`/`<messages>` | NewsHeader/Message | нет | нет | T24/T25 stub |

## 6. Тесты и фикстуры по подсистемам

- **records** (`native/records/testdata`): alltrades-*, candles-*, order-*, trade-*,
  stoporder-*, position-*, transact-*, portfolio-*-dll, quotation/quote-live,
  secparam-live, startup-demo, main1-demo, e07-tail-live, misc-live, sysparams-inject,
  boards/markets/candlekinds-demo, sec_info_upd-dll, securities1-demo.
- **native** (`native/testdata`): snapshot-tail-live(.hex/.xml), secinfo-live, secspec-request,
  service-dll, connect-options-live, recover-* (session).
- **session** (`native/session/testdata`): recover-short/long-live (.hex/.xml/.md).
- **Тесты**: 41 файл `*_test.go` в `native`, `records`, `xmlcompat`, `session` (точный
  список — в репозитории; ключевые из них указаны в таблицах выше).

## 7. Открытые I-точки и блокеры

1. **T14 ticks**: запись/сессия/команда есть, XML `<ticks>` и DoD — ждут S-серию (10:00+ МСК).
2. **T19 moveorder/makeordown**: payload не снимается без живой активной заявки; после S-серии.
3. **T21 limits**: `<clientlimits>` XML — синтетика (D), ждёт O-фикстуру; RQ_FORTS_FIRM_MONEY 47
   не реализован (формат I, portfolios/limits.md).
4. **T22 portfolios**: `portfolio_tplus/forts/united/mct`, typed client-API и формулы
   equity/cover/init_req/maint_req/unrealized_pnl — I (зафиксировано в коде и portfolios.md).
5. **T23–T25**: только каркас (6a57f75), явные I-ошибки; ждут RE-2/RE-3 материалы.
6. **T26–T28**: не начаты (change_pass, options, RPS/REPO).
7. **T29**: 26/43 XML-команд в Connector; native backend `server/`, матрица 45 имён и тексты
   ошибок (Task 29 Step 1/1b) — не сделаны; нет `newreport4`/`cancelreport4` (task-index `8.1).
8. **Windows/Wine**: нативный клиент сборками `make server_build/queues_build` и `make verify`
   проверяется; отдельного прогона native на Windows не было (не проверено).

## 8. Ссылки

`docs/protocol/connect.md`, `dictionaries.md`, `commands.md`, `alltrades.md`, `records.md`,
`history.md`, `orders.md`, `positions.md`, `limits.md`, `portfolios.md`;
план — `docs/superpowers/plans/2026-11-04-native-transaq-client.md`;
индекс — `docs/deepseek/task-index.md`.
