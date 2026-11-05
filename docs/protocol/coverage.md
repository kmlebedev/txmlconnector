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
| 23 | max buy/sell, cln_sec_permissions, leverage | 5 | **partial** | local render 3 команд + `<marketord>` (b29b757/6867d83); точная формула maxbuy/maxsell по R_SEC_RATE 0x0a (a217bc1); permissions/rates — I (FUN_18001a700) |
| 24 | news/get_old_news/get_news_body | 6 | **partial** | 0x35/0x36 парсеры+splitter, `<news_header>` O-побайтно, RQ_GETNEWS payloads и MIN-id диапазон (7484ffc + fix), битый 0x36 package skip; `<news_body>` — I (task-56) |
| 25 | messages (R_MSG 0x01) | 6 | **done** | unsized 0x01, batch одного RQ_MAIN, `<messages>` O-побайтно (73a4acc/6cb9697); `<date>` = WALL-время (fix после ревью) |
| 26 | change_pass | 6 | **done** | RQ_CHANGE_PWD 3, payload/SHA-1, replies, fatal empty reply, local texts (7427b10); живьём не выполнялось (S-негатив) |
| 27 | options | 7 | **partial** | RQ 149/153/150/151 payloads+reply (empty reply=failure, 00=success), 149 O e2e и exact DLL error text, dedup family callbacks (fix после ревью); гейт 0x45 только 153/151 — I, mapping/XML — D/I |
| 28 | RPS/REPO/отчёты | 8 | **partial** | payloads newrpsorder/newrepoorder/cancel O-побайтно, 6 команд, ошибки, D-literal XML (b9f305a); newrepoorder known fields (repoterm/reporate/discounts/block) encoded, value/refundrate/matchref/cpfirmid/settlecode — explicit I; newmrepoorder/newreport4, records/XML negdeal/report и RPS-специфичные reply-тексты — I |
| 29 | XML-коннектор целиком, native backend | 9 | **partial** | `dllCommands` = 45 имён; matrix-тест и pending {moveorder,makeordown} (043d7c9/6aa832f); `server.Backend dll|native` + `TC_BACKEND`, `TC_BACKEND=native make server` без Wine (043d7c9/5aea9d5); TestServerErrorTexts + errors.md сделаны (6aa832f); RE-4 на демо — pending после S |
| 30 | Матрица покрытия, soak, выпуск | 9 | **in progress** | этот документ; soak/выпуск — после 29 |

Фазы 0–2 закрыты по AGENTS.md (коммиты в истории ветки, PR #47/#48 — в main).

## 2. XML-команды Connector (`dllCommands`, 45 имён)

Всего в `dllCommands` **45 имён**; реализовано **43** обработчика, pending — `moveorder`, `makeordown`
(живая активная заявка, T19). Специальные (`wait=false`): connect, disconnect, authenticate, set_csp,
get_connector_version. Расхождение плана «45 vs 43» закрыто (T28 добавил `newreport4`/`cancelreport4`).

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
| get_max_buy_sell_tplus | done (local) | локальный рендер из сторов | O/D | `TestMarginMatchDLL`, `margin-dll.xml` | MCT -> ошибка; точная формула по R_SEC_RATE 0x0a (a217bc1) |
| get_max_buy_sell | done (local) | локальный рендер | O/D | `TestMarginMatchDLL`, `TestMarginMCTBehavior` | MCT -> success 0/0; cross/шорт по margin.md (a217bc1) |
| get_cln_sec_permissions | done (local) | локальный рендер | O/D | `TestClnSecPermissionsMatchDLL` | marketable из 0x0e; права/ставки I (FUN_18001a700) |
| get_old_news | done | RQ_GETNEWS 73 (mode 1) | O/D | `TestOldNewsPayloadMatchesDLL`, `news-inject.hex` | заголовки — события 0x35/0x36; O-пейлоады 713e913e0201 / 017d217d0201 |
| get_news_body | done (команда) | RQ_GETNEWS 73 (mode 2) | O/D | `TestGetNewsBodyRequest`, `TestNewsErrors` | payload 217d217d0102; `<news_body>` — I (task-56) |
| change_pass | done | RQ_CHANGE_PWD 3 | O/D | `TestChangePasswordPayloadMatchesDLL/Command/ReplyErrors/LocalErrors/EmptyReplyFatal` | 56-Б SHA-1 payload; пустой reply фатален/закрывает сессию; S-негатив (живьём не выполнялось) |
| get_option_families | done | RQ_OPTION_FAMILIES 149 | O/D | `TestConnectorOptionFamilies`, `option-families-request/reply-*.hex` | payload 001a; пустой/непустой C-string reply (O); XML — D/I |
| get_family_strikes | partial (I) | RQ_FAMILY_STRIKES 153 | D/I | `TestConnectorFamilyStrikesErrors` | mat_date ошибки D+O; family mapping и гейт 0x45 — I, запрос не отправляется |
| get_options | partial (I) | RQ_CERTAIN_OPTIONS 151 | D/I | `TestConnectorGetOptions` | Z25 payload и local «No seccodes found»; XML/данные I |
| set_csp | done | — (local) | D/O | `TestCommandDispatch`, matrix | `<csp type>`: NONE/MS/CP/SC/MP/MPSSL/CPBB/CPBB_2012_*; без csp — success; при соединении — CP1251 disconnect-текст; CSP не устанавливается (I, вне T29) |
| authenticate | partial (I) | — (local) | D/O | `TestCommandDispatch`, matrix | по умолчанию «Illegal context for "sendpin" command»; настроенный провайдер/pin/async-колбэки — I |
| newrpsorder | done | RQ_TRANSACT 14 (TRN 0x04) | O/D | `TestRPSPayloads`, `rps-transact-newrpsorder.hex` | 30-Б payload побайтно; repoterm/reporate offsets I |
| newrepoorder | done | RQ_TRANSACT 14 (TRN 0x0e) | O/D | `TestRPSPayloads`, `rps-transact-newrepoorder.hex` | 39-Б payload побайтно; repo-поля I |
| newmrepoorder | partial (I) | RQ_TRANSACT 14 | D/I | `TestRPSErrors` | payload не восстановлен -> explicit I |
| cancelnegdeal | done | RQ_TRANSACT 14 (0x08+L) | O/D | `TestRPSPayloads`, `rps-transact-cancel.hex` | 2-Б payload 0810 |
| newreport4 | partial (I) | RQ_TRANSACT 14 | D/I | `TestRPSErrors` | ustrade_leg layout не восстановлен -> explicit I |
| cancelreport4 | done | RQ_TRANSACT 14 (0x08+L) | O/D | `TestRPSPayloads`, `rps-transact-cancel.hex` | payload как cancelnegdeal; различие TRN-кода I |

### 2.3 Не реализовано в Connector (default → message not implemented)

makeordown, moveorder (2 имени; T19 — ждут живую S-серию).

## 3. Записи RQ_MAIN (splitter `records.Records` + декодеры `native/events*`)

| Tag | Запись | Splitter | Декодер/состояние | XML-форма | Факт | Тесты/фикстуры |
|---|---|---|---|---|---|---|
| 0x01 | R_MSG | unsized walker (C-строки) | decodeMessage → Message, батч `<messages>` | `<messages>` | O | `message-inject.hex`, `TestMessagesMatchDLL/UrgentAndTimeBias` |
| 0x02 | R_CLN_NEW (sized) | sizedTags | decodeClientNew → ClientAccount + pendingOrders | `<client>` (конец загрузки) | O | `snapshot-tail-live*`, `TestSnapshotTailMatchesDLL` |
| 0x03 | R_ORD_NEW (sized) | sizedTags | decodeOrderNew → Order (фильтр client id) | `<orders>` | O | `order-new-*`, `TestConnectorOrders` |
| 0x04 | R_TRD (sized) | sizedTags | decodeClientTrade → ClientTrade | `<trades>` | O | `trade-*`, `TestConnectorTradesCallback` |
| 0x05 | state | special | state-only (пусто) | — | D | `TestStateOnlyRecords` |
| 0x06 | R_QUOTATION | splitter по маске | decodeQuotation → Quotation | `<quotations>` | O | `quotation-live*`, `quotations-dll.xml` |
| 0x07 | R_QUOTES | splitter | decodeQuotes → Quotes | `<quotes>` | O | `quote-live*`, `quotes-dll.xml` |
| 0x08 | R_ALLTRADE (sized) | sizedTags | decodeAllTrade → Trade | `<alltrades>` | O | `alltrades-*` |
| 0x09 | R_HISTORY | special (unsized) | decodeCandles → Candles | `<candles>` | O | `candles-*` |
| 0x0a | R_SEC_RATE | special (W+L) | decodeSecRate → Client.marginRates (V, копейки) | — (только state) | D/O | `TestParseSecRate`, `TestMarginSnapshotIncludesRate`, `TestMarginMatchDLL` |
| 0x0b–0x0d | R_SEC_ZALOG/OBLIG/ONOFF | attrTags 3 Б | state-only (нет события) | — | D | `TestRecordsAttributeUpdates` |
| 0x0e | R_SEC_MKT_PERMIT | attrTags 3 Б | decodeSecMktPermit → MarketOrd + флаг бумаги | `<marketord>` | O/D | `marketord-inject.hex`, `TestMarketOrdEvent/UnknownSecID/EveryRecord/DuringLoad` |
| 0x10 | R_ORD_MODIFY (sized) | sizedTags | decodeOrderModify → Order (все mask-ветки) | `<orders>` (один финальный) | O | `order-modify-*`, `TestOrderModifyBatchOneCallback` |
| 0x18 | R_OVERNIGHT | 1 байт | decodeOvernight → Overnight | `<overnight>` | O | `TestOvernightRecordReachesStatus` |
| 0x28 | R_SEC_NEW (sized) | sizedTags | нет декодера (каталог из RQ_STARTUP) | — | D/I | `TestParseSecNewSnapshot` (records) |
| 0x34 | R_BOARD (sized) | sizedTags | нет декодера (каталог из RQ_STARTUP) | — | D/I | — |
| 0x35 | R_NEWS | unsized walker (C-строки) | decodeNewsHeader → NewsHeader + store | `<news_header>` | O | `news-inject.hex`, `TestNewsHeadersMatchDLL` |
| 0x36 | R_NEWS_UPLOAD | unsized walker (C-строки) | decodeNewsUpload → NewsHeader + zlib body в стор | `<news_header>` | O | `news-inject.hex`; `<news_body>` I (task-56) |
| 0x3a | R_ORD_RENEW (sized) | sizedTags | нет декодера | — | D/I | — |
| 0x3b | R_CLIENT_ASSET (sized) | sizedTags | decodeClientAsset → MoneyPosition/SecPosition/Forts* | `<positions>/<sec_positions>/<forts_positions>` | O | `position-*`, `snapshot-tail*` |
| 0x3d | R_DATE | special (L) | state-only | — | D | `TestParseEndOfLoadRecords` |
| 0x54 | R_STOP (sized) | sizedTags | decodeStopOrder → StopOrder (фильтры client/secid) | `<stoporders>` (mask-aware) | O | `stoporder-*`, `TestStopOrdersMatchDLL` |
| 0x5b | R_SYSPARAMS | L size + blob | state-only (blob I) | — | D/I | `sysparams-inject.hex`, `TestSizedMiscRecords` |
| 0x5c | AmBOOK | splitter по flags | state-only (нет события) | — | D | `misc-live*`, `TestMiscLiveRecords` |
| 0x5e | R_SEC_PARAM (sized) | sizedTags | decodeSecParam → SecInfoUpdate | `<sec_info_upd>` | O | `secparam-live*`, `TestSecInfoUpdMatchesDLL` |
| 0x5f | R_WRAPPER (sized) | sizedTags → nested | вложенные записи (0x6e skip) | — | O/D | `TestE07TailHasNoUnknownRecords` |
| 0x62,0x67 | MarCat/Unicat | 2 Б | state-only | — | D | `TestRecordsStopsAtUnknownLayout` (контекст) |
| 0x6f | R_FAMILY | unsized walker | decodeOptionFamily → OptionFamily (state+event) | `<option_families>` (D/I) | D | `TestOptionsRecordsSplit`, `TestOptionFamilyEvent` |
| 0x71 | R_END_OF_FAMILY | unsized walker | decodeEndOfFamily → EndOfFamily | `<options_failed>` (D/I) | D | `TestOptionsRecordsSplit` |
| 0x78 | R_STRIKES | unsized walker | decodeOptionStrikes → OptionStrikes | `<family_strikes>` (D/I) | D | `TestOptionsRecordsSplit` |
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
| `<marketord>` | MarketOrd | `MarketOrdXML` | `marketord-dll.xml` | O |
| `<portfolio_tplus>`/`<portfolio_forts>`/`<united_portfolio>`/`<portfolio_mct>` | — | только литеральные скелеты, не в Render | — | I (T22 remaining) |
| `<ticks>` | Tick | рендер не реализован | нет | T14 partial |
| `<news_header>` | NewsHeader | `NewsHeaderXML` | `news-dll.xml` | O |
| `<news_body>` | NewsBody | `NewsBodyXML` (D-форма) | нет O | I (task-56) |
| `<messages>` | Messages batch | `MessagesXML` | `message-dll.xml` | O |
| `<option_families>`/`<family_strikes>`/`<options_failed>` | OptionFamily/OptionStrikes/EndOfFamily | `OptionFamiliesXML`/`FamilyStrikesXML`/`OptionsFailedXML` | нет O | I (D-литералы) |
| `<negdeal>` | RPSTradeForExecution | `NegDealXML` | нет O | I (D-литералы) |
| `<report>` | ExecutionReport | `ReportXML` | нет O | I (D-литералы) |

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

1. **T14 ticks**: S-серия на проде подтвердила — тики приходят **RQ_MAIN `0x5a` TICK**
   (`W secid, W board, G tradeno, L time, L price, L quantity, B period, B buysell, L X, L Y`,
   накопление tradeno/time), `RQ_TICKS(13)` не используется; `subscribe_ticks` — board/seccode/tradeno,
   `unsubscribe_ticks` — Unknown command. Осталось: rework T14 (декодер 0x5a, XML по S-форме, без
   поллинга 13) — в работе.
2. **T19 moveorder/makeordown**: payload — только живая активная заявка; ждёт демо-серию с торговым
   циклом (согласие есть).
3. **T21 limits**: `get_client_limits` S-проверен (C>S 43 payload, `<clientlimits>`); O-фикстура — после
   S; `RQ_FORTS_FIRM_MONEY 47` не восстановлен (I).
4. **T22 portfolios**: `get_portfolio`/`get_united_portfolio`/`get_mc_portfolio` — S-проверены
   (local render), `get_portfolio_mct` — `{8}`, `get_portfolio_tplus/forts` — Unknown command; typed
   client-API, формулы и остальные формы — I.
5. **T23**: local render 3 команд + `<marketord>`; maxbuy/maxsell — точная формула (R_SEC_RATE 0x0a +
   open_balance/lots, a217bc1, побайтно margin-dll.xml). I: permissions/rates (FUN_18001a700 нет в RQ_MAIN),
   источник R и начальный V.
6. **T24**: `news_header`/`news_body` **полностью S-сняты** (reply 73 пуст, 0x36 flags=1/2, lastId=MIN);
   осталось: доработка кода news_body по S. **T25**: done O-побайтно (wall-время + bias).
7. **T26**: change_pass done (payload SHA-1, reply-коды); в работе ревью-P1 — терминальность fatal reply;
   I: политика conn+0x279. **T27**: 149 O, 151 payload D, гейт 153/151, XML D/I (O-форм нет).
   **T28**: 4 O-payload, D-XML I, `newmrepoorder`/`newreport4` — I.
8. **T29**: native backend/`TC_BACKEND`/`make server` без Wine, матрица 45 имён (43 обработчика, pending
   `{moveorder, makeordown}`), error texts/errortable, `set_csp`/`authenticate` — сделано; осталось:
   RE-4 на живом сервере, полный matrix после T19, синхронизация доков.
9. **Windows/Wine**: нативный клиент проверяется сборками `make server_build/queues_build` и `make verify`;
   отдельного прогона native на Windows не было (не проверено).

## 8. Ссылки

`docs/protocol/connect.md`, `dictionaries.md`, `commands.md`, `alltrades.md`, `records.md`,
`history.md`, `orders.md`, `positions.md`, `limits.md`, `portfolios.md`;
план — `docs/superpowers/plans/2026-11-04-native-transaq-client.md`;
индекс — `docs/deepseek/task-index.md`.
