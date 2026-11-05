# Индекс задач плана нативного TRANSAQ-клиента

Служебный документ команды DeepSeek Harness. Источник задач — план
[`docs/superpowers/plans/2026-11-04-native-transaq-client.md`](../superpowers/plans/2026-11-04-native-transaq-client.md)
(636 строк, 30 задач, 10 фаз 0–9). План не переписывается: расхождения плана с кодом и деревом
зафиксированы в §8, а не «исправлены по ходу».

**Как собран индекс.** План прочитан целиком; прочитаны [`tools/re/README.md`](../../tools/re/README.md),
[`.kimi-code/skills/go-engineer/SKILL.md`](../../.kimi-code/skills/go-engineer/SKILL.md),
[`docs/ROADMAP-GO.md`](../ROADMAP-GO.md), `docs/protocol/{connect,dictionaries,commands,alltrades}.md`;
наличие файлов проверено `git ls-files native tools docs`, `ls native/*/`, `ls tools`, точечными
`grep`/`read`. Состояние зафиксировано на `main` = `b940f9a` (Merge PR #45). Незакоммиченные файлы
рабочего дерева (`AGENTS.md`, `docs/deepseek/*`, правки `Makefile`/`.gitignore`) как «существующие в
main» не учитывались.

**Обозначения в таблицах:**
- `RE-1` — Ghidra-декомпиляция — **доступна сейчас** (скрипты в `tools/re/ghidra`, DLL — в корне).
  В песочнице DSH нужны `JAVA_HOME=openjdk@25`, `mkdir -p "${TMPDIR:-/tmp}/dsh-ghidra-home"` и
  `JAVA_TOOL_OPTIONS=-Duser.home=…`, а проект Ghidra — в `${TMPDIR:-/tmp}/txc-ghproj` ВНЕ workspace:
  Ghidra 12.2 отвергает '.'-пути (`.re-run/ghproj`); симлинк `.re-run/ghproj` — для удобства.
  Проверено 2026-11-04: импорт DLL succeeded, ~3 м 50 с (см. environment.md).
- `RE-2` (живой захват DLL) и `RE-3` (оракул) — **доступны** (2026-11-04: Wine 11.21 установлен,
  `run_dll.sh` + `get_connector_version` проверены). RE-2 требует `TC_PASSWORD` и окно демо-сервера,
  RE-3 — записанную сессию + `TC_PASSWORD`. `RE-4` (живая проверка Go) — **пока нет**: нужен
  `TC_PASSWORD`. Секреты/Wine-экспорты — `~/re/demo.env` (шаблон `tools/re/demo.env.example`), в
  таблицах ниже вместо «(нет)» указано, что именно нужно. Подробности —
  `docs/deepseek/environment.md`.
- «выв.» — зависимость выведена из общих файлов/порядка, а не написана в плане дословно.
- `T#` — id карточки на доске для задачи # плана (`blocked_by` принимает id карточек, не номера).

---

## 1. Сводка

| Параметр | Значение |
|---|---|
| Задач в плане | 30 (нумерация 1–30 без пропусков) |
| Фаз | 10: 0 «Фундамент», 1 «Сеанс», 2 «Рыночные данные», 3 «История и тики», 4 «Торговля», 5 «Позиции и портфели», 6 «Новости, сообщения, пароль», 7 «Опционы», 8 «РПС/РЕПО», 9 «Интеграция» |
| Обязательный старт | Фаза 0 (задачи 1–4); задачи 1 и 2 независимы, 3 зависит от 2, 4 — после 3 |
| Review Focus плана | 5 штук: №1 и №5 → задача 3; №2 → задача 15; №3 → задача 5; №4 → задача 9 |
| Задач с RE-шагами | 24 из 30; RE-2 нужен в 17 задачах, RE-3 в 12, RE-4 в 10 (см. таблицу §4) |
| Файлов, в которые пишут несколько задач | 18 строк (21 путь; см. §5); главные — `native/xmlcompat/render.go` (создаёт задача 4, дописывают 13 задач), `native/connector.go` (12), `native/client.go` (4), `native/events.go` (все задачи-декодеры) |
| Задач, которые нельзя вести параллельно между собой | 14 задач «полосы render.go/connector.go» (включая создание `render.go` в задаче 4), цепочка 2→3→4 и 29→30 (см. §7) |

---

## 2. Фазы: задачи, результат, что уже есть в `main`

| Фаза | Задачи | Результат фазы (из плана) | Что уже есть в `main` (проверено) | Чего нет |
|---|---|---|---|---|
| 0. Фундамент | 1–4 | Инструменты RE на Go (`tools/tcproxy`, `native/internal/replay`, `tools/replay-server`), пакеты `native/model`/`native/records`, событийный API `Client.Run`, шаблон `Render`/`checkDLL` | `native/wire` (`codec.go`, `frame.go`), `native/cryptoprofile`, `native/session` (`login.go`, `requests.go` — 11 констант RQ_*), `native/xmlcompat` (`catalog.go`, `records.go`, `alltrade.go`, `security.go`+`securityxml.go`, `startup.go`, `timezone.go`, 12 файлов `testdata/`), `native/client.go` (`Dial`, `Poll` только сделки, `SubscribeAllTrades`, `Stats`), `native/connector.go`, `native/natsout`, `tools/{wire-decode,reference-harness,native-login,native-trades,re}` | `native/model`, `native/records`, `native/events.go`, `native/xmlcompat/render.go`, `native/internal/{replay,testserver}`, `tools/{tcproxy,replay-server}` |
| 1. Сеанс | 5–7 | Переподключение/восстановление, служебные команды и параметры `connect`, сообщения конца загрузки `<client>/<positions>/<overnight>` | `docs/protocol/connect.md` и `commands.md`; `session.Conn`/`Request`/`exchange`; константы `RqRelogin`/`RqRecover` есть, реализация — нет; `fakeServer` (тестовый, в `native/connector_test.go`) умеет `drop()`, но не экспортирован | `native/session/recover.go`, `native/recover.go`, `native/client_service.go`, `native/client_accounts.go`, `native/records/client.go`, `native/model/client.go`, `native/internal/testserver/testserver.go`, `native/testdata/` |
| 2. Рыночные данные | 8–12 | `<sec_info_upd>`, `<quotations>`, `<quotes>`, `<sec_info>`, остальные записи снапшота | `docs/protocol/alltrades.md` (RQ_SUBSCRIBE, биты масок, R_ALLTRADE, R_SEC_RATE/ZALOG/OBLIG/ONOFF), `docs/protocol/dictionaries.md` (RQ_STARTUP, R_SEC_NEW, производные поля, R_SEC_PARAM: маски, сеттеры, XML-форма `<sec_info_upd>`; эмиттера нет); теги 0x0a (R_SEC_RATE) и 0x0b–0x0e (атрибуты бумаги) уже разбираются `xmlcompat.Records`, но событий не порождают | `native/records/{quotation,quote,secparam,secinfo,misc}.go`, `native/model/{quotation,quote,secinfo,misc}.go`, `native/client_subscribe.go`, `native/client_secinfo.go`; констант RQ_SEC_SPEC (15)/RQ_INQUIRE_SECURITY (111) нет; фикстур `secparam-live.hex.gz`, `sec_info_upd-dll.xml.gz` и quotation/quote-live в репозитории нет |
| 3. История и тики | 13–14 | `gethistorydata`, `subscribe_ticks` | — (ничего по теме: нет констант RQ_HISTORY 11 / RQ_HISTORY_EXT 148 / RQ_TICKS 13 / RQ_SUBSCRIBE_TICKS 120, нет декодеров, нет `docs/protocol/history.md`) | `native/session/{history,ticks}.go`, `native/records/{candles,ticks}.go`, `native/model/{candle,tick}.go`, `native/client_{history,ticks}.go` |
| 4. Торговля | 15–19 | RQ_TRANSACT, заявки, сделки клиента, стоп-/условные заявки, `moveorder` | — (нет `session/transact.go`, нет констант RQ_TRANSACT 14 / RQ_TRNS_IN_PROGRESS 156 / RQ_ENFORCE_TRN_RELEASE 157; есть `session.Conn.Request`/`exchange`, на которых это строится) | `native/session/transact.go`, `native/model/{transaction,order,clienttrade,stoporder}.go`, `native/records/{order,clienttrade,stoporder}.go`, `native/client_{orders,stoporders}.go` |
| 5. Позиции и портфели | 20–23 | позиции, лимиты, портфели, max buy/sell, разрешения | XML-типы в `client/commands`: `MoneyPosition`, `SecPosition`, `FortsPosition`, `UnitedPortfolio`, `UnitedEquity` (используются только XML-путём `xmlcompat`) | `native/model/{position,limits,portfolio,margin}.go`, `native/records/{positions,limits,portfolio,margin}.go`, `native/client_{positions,portfolio,margin}.go`, `native/session/{limits,portfolio,margin}.go` |
| 6. Новости, сообщения, пароль | 24–26 | новости, текстовые сообщения, `change_pass` | — (нет констант RQ_GETNEWS 73 / RQ_CHANGE_PWD 3, нет декодеров R_MSG 0x01) | `native/session/{news,changepwd}.go`, `native/records/{news,message}.go`, `native/model/{news,message}.go`, `native/client_{news,password}.go` |
| 7. Опционы | 27 | семейства, страйки, опционы | — (обработчики и типы запросов 149/153/150/151 упомянуты в плане, в коде отсутствуют) | `native/session/options.go`, `native/records/options.go`, `native/model/option.go`, `native/client_options.go` |
| 8. РПС/РЕПО | 28 | раздел 5 PDF | — (нет `session.Transact` задачи 15, нет RQ_RPSREPO_SUBSCR 72 / RQ_GET_RPSREPO 82 / RQ_REPORT_INFO 21 / RQ_GET_REPORT 22) | `native/records/rps.go`, `native/model/rps.go`, `native/client_rps.go` |
| 9. Интеграция | 29–30 | XML-коннектор целиком, gRPC-сервер без Wine, матрица покрытия | `native/connector.go`: `dllCommands` — **43** имени (без `newreport4`, `cancelreport4`), реализованы `connect`, `disconnect`, `server_status`, `get_connector_version`, `get_securities`, `subscribe`/`unsubscribe` (только `<alltrades>`); `server/server.go` `Config` без `Backend`; `examples/grpc-client`; `docs/protocol/{connect,commands,dictionaries,alltrades}.md`; в README есть раздел «Native Go client», но он про gRPC-клиент (`make client`), примера `native.Dial/Subscribe/Run` нет | `server.Config.Backend` и `TC_BACKEND`, `native/connector_matrix_test.go`, `docs/protocol/errors.md`, `docs/protocol/coverage.md`, `tools/native-soak` |

---

## 3. Процедуры RE и доступность сейчас

Полные команды и ловушки — [`tools/re/README.md`](../../tools/re/README.md). Пока не сделана задача 1,
эталоном остаются Python-версии `tools/re/py/proxy.py` и `tools/re/py/replay_server.py`; Go-версии
появляются в задаче 1.

| Процедура | Что даёт | Доступность сейчас | Чем |
|---|---|---|---|
| RE-1 Декомпиляция | Чтение полей записей и запросов, карты обработчиков | **Да** (2026-11-04 импорт DLL проверен: Import succeeded ~3 м 50 с) | Ghidra 12.2 headless, `tools/re/ghidra/*.java`, `tools/re/py/dll_maps.py`; проект — `${TMPDIR:-/tmp}/txc-ghproj` (Ghidra отвергает '.'-пути; симлинк `.re-run/ghproj`), DLL в корне |
| RE-2 Живой захват DLL | Байты запросов/ответов, XML-колбэки DLL | **Да** (2026-11-04: `TC_PASSWORD` в `~/re/demo.env`, согласие пользователя получено; окно демо-сервера 09:50–23:50 МСК); захваты — в `.re-run/` (gitignored) | Wine 11.21 (`~/re/wine/.../wine`, `~/re/wine-prefix`, проверено `run_dll.sh`), `tools/tcproxy`, `tools/re/py/proxy.py`, `tools/wire-decode` |
| RE-3 Оракул | XML DLL для записей, которых нет на демо (сделки, заявки, позиции, РПС) | **Да**, при наличии записанной сессии и `TC_PASSWORD` | Wine 11.21 + `replay_server.py` (или Go-версия из задачи 1), `TC_PASSWORD` из `~/re/demo.env` |
| RE-4 Живая проверка Go | Сверка Go-клиента с сервером через прокси | **Да** с 2026-11-04 (`TC_PASSWORD` в `~/re/demo.env`, согласие пользователя; окно демо-сервера) | `TC_LOGIN`/`TC_PASSWORD` + демо-сервер `tr1-demo5.finam.ru:3939` + `tools/tcproxy` |

Практическое следствие для планирования: RE-2/RE-3/RE-4 доступны (Wine есть, `TC_PASSWORD` в
`~/re/demo.env`, согласие пользователя получено 2026-11-04); для RE-3 нужна записанная сессия (или
захват пользователя вне git). Задачи, у которых в шагах есть RE-2/RE-3 **и
нет фикстуры в репозитории**, закрываются по DoD только после снятия фикстур (живой захват или оракул)
либо остаются `blocked` с явной причиной. Список таких задач — §8, пункты 11–13, 17.

---

## 4. Таблица задач 1–30

Столбец «Write-scope» — только то, что задача **создаёт/меняет** (репо-относительные пути из секции
Files; тесты включены; `Exists`-файлы не повторяются). Столбец «RE» — процедуры из шагов задачи и их
доступность сейчас.

| № | Задача (название из плана) | Фаза | Зависимости | Write-scope | RE | Приёмочные тесты | Commit (из плана) | RF |
|---|---|---|---|---|---|---|---|---|
| 1 | Прокси и проигрыватель на Go | 0 | — (независима от кода `native`; Python-эталон уже в `tools/re`) | `tools/tcproxy/main.go`, `tools/tcproxy/main_test.go`, `native/internal/replay/replay.go`, `native/internal/replay/replay_test.go`, `tools/replay-server/main.go`; `tools/re/README.md` (шаг 8) | — (перенос инструментов; py-версии — эталон) | `TestProxyForwardsAndLogs`, `TestReplayServesRecordedSession` | `feat(re): logging proxy, replay server and Ghidra scripts in the repository` | — |
| 2 | Пакеты `native/model` и `native/records` | 0 | — (рефакторинг существующего кода; должно завершиться до 3) | Create `native/model/{decimal,symbol,trade,security,dictionary}.go`, `native/model/decimal_test.go`; Move `native/xmlcompat/{records,startup,security,alltrade,catalog,timezone}.go` + их тесты + `testdata/*` → `native/records/` (см. §8 п.5); Modify `native/xmlcompat/*.go`, `native/client.go`, `native/connector.go`, `native/natsout/natsout.go`, `tools/*` | — | `TestDecimalString`; `go test -race ./...`; `TXML_DEMO_MAIN/TXML_DEMO_XML go test -run SecuritiesFull ./native/records` (существующий `TestSecuritiesFullCapture`) | `refactor(native): split binary records and typed model out of xmlcompat` | — |
| 3 | Событийный API `Client.Run` | 0 | 2 (`Consumes records.Records`, `records.Catalog`, `model`) | Create `native/model/event.go`, `native/events.go`, `native/internal/testserver/testserver.go`; Modify `native/client.go`, `native/client_test.go`, `native/connector.go`, `native/connector_test.go` (вынести `fakeServer`), `tools/native-trades/main.go` | — | `TestRunReportsUnknownRecord`, `TestConcurrentRequestsDuringRun` | `feat(native): event API Client.Run with a decoder registry` | **RF1, RF5** |
| 4 | Шаблон задачи «семейство» и сверка XML | 0 | 2, 3 (`records.Catalog`, `model.Event`; шаблон для 5–28) | Create `native/records/oracle_test.go`, `native/xmlcompat/render.go` | — | `TestRenderAllTradesThroughRender` | `feat(xmlcompat): single Render entry point and DLL oracle test helper` | — |
| 5 | Переподключение и восстановление (RQ_RELOGIN 63, RQ_RECOVER 64) | 1 | 3 (testserver), 4 (`records.Catalog`, Render); добавляет `testserver.Drop()` (выв.) | Create `native/session/recover.go`, `native/session/recover_test.go`, `native/recover.go`, `native/recover_test.go`; Modify `native/client.go`, `native/connector.go`, `native/internal/testserver/testserver.go` (Drop, выв.), `docs/protocol/connect.md` | RE-1; RE-2 (Wine есть) — эксперимент E05; RE-4 (нужен TC_PASSWORD) | `TestRecoverPayloadMatchesDLL`, `TestRecoverResubscribesBySymbol` | `feat(native): reconnect and recover like the DLL` | **RF3** |
| 6 | Служебные команды и параметры `connect` | 1 | 3, 4 (выв.: `Run`, `Render`); RE-3 — существующая фикстура alltrades | Create `native/client_service.go`, `native/client_service_test.go`; Modify `native/connector.go`, `native/xmlcompat/render.go`, `docs/protocol/commands.md`, `docs/protocol/connect.md` (шаг 0) | RE-1; RE-2 (Wine есть); RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestConnectOptionsMatchDLL`, `TestServiceCommandsMatchDLL`, `TestServerTimeDifference` | `feat(native): get_servtime_difference, get_server_id, get_markets` | — |
| 7 | Сообщения конца загрузки: `<client>`, `<positions>`, `<overnight>`, `<union>` | 1 | 3, 4 (выв.); фактически `<positions>` требует типов задачи 20 — см. §8 п.10 | Create `native/records/client.go`, `native/records/client_test.go`, `native/model/client.go`, `native/client_accounts.go`; Modify `native/xmlcompat/render.go`, `native/connector.go` | RE-1; RE-2 (Wine есть); RE-4 (нужен TC_PASSWORD) | `TestSnapshotTailMatchesDLL` | `feat(native): client accounts, overnight and union at the end of the load` | — |
| 8 | `<sec_info_upd>` из R_SEC_PARAM (0x5e) | 2 | 3 (реестр), 4 (Render) | Create `native/records/secparam.go`, `native/records/secparam_test.go`, `native/model/secinfo.go`; Modify `native/xmlcompat/render.go`, `native/events.go` | — (форма/маски уже разобраны в #44: `docs/protocol/dictionaries.md`, статус D/S; шагов RE-1/2/3 в плане нет) | `TestSecInfoUpdMatchesDLL` | `feat(native): sec_info_upd from R_SEC_PARAM` | — |
| 9 | `<quotations>` — подписка (маска 0x01) и запись 0x06 | 2 | 3, 4 (выв.) | Create `native/records/quotation.go`, `native/records/quotation_test.go`, `native/model/quotation.go`, `native/client_subscribe.go`; Modify `native/client.go`, `native/connector.go`, `native/xmlcompat/render.go` | RE-1; RE-2 (Wine есть); RE-3 (Wine есть; нужна запись+TC_PASSWORD); RE-4 (нужен TC_PASSWORD) | `TestQuotationsMatchDLL`, `TestSubscribeMasksMatchDLL`, `TestQuotationUsesBoardDecimals` | `feat(native): quotations subscription and updates` | **RF4** |
| 10 | `<quotes>` — стакан (маска 0x02) и запись 0x07 | 2 | 9 (`client_subscribe.go`), 3, 4 | Create `native/records/quote.go`, `native/records/quote_test.go`, `native/model/quote.go`; Modify `native/client_subscribe.go`, `native/xmlcompat/render.go` | RE-1; RE-2 (Wine есть); RE-4 (нужен TC_PASSWORD) | `TestQuotesMatchDLL`, `TestSubscribeQuotesMask` | `feat(native): order book quotes` | — |
| 11 | `get_securities_info` → `<sec_info>` (RQ_SEC_SPEC 15, RQ_INQUIRE_SECURITY 111) | 2 | 8 (`native/model/secinfo.go`), 3, 4 | Create `native/session/secspec.go` + тест, `native/records/secinfo.go` + тест, `native/client_secinfo.go`; Modify `native/model/secinfo.go`, `native/xmlcompat/render.go`, `native/connector.go` | RE-1; RE-2 (Wine есть); RE-4 (нужен TC_PASSWORD) | `TestSecSpecPayloadMatchesDLL`, `TestSecInfoMatchesDLL` | `feat(native): get_securities_info` | — |
| 12 | Остальные записи снапшота и обновлений справочников | 2 | 3, 4 | Create `native/records/misc.go` + тест, `native/model/misc.go` | RE-1; RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestFullLiveSessionHasNoUnknownRecords` | `feat(records): remaining snapshot and dictionary update records` | — |
| 13 | `gethistorydata` → `<candles>` (RQ_HISTORY 11, RQ_HISTORY_EXT 148) | 3 | 3, 4 (выв.) | Create `native/session/history.go` + тест, `native/records/candles.go` + тест, `native/model/candle.go`, `native/client_history.go` + тест; Modify `native/xmlcompat/render.go`, `native/connector.go` | RE-1; RE-2 (Wine есть); RE-4 (нужен TC_PASSWORD) | `TestHistoryPayloadMatchesDLL`, `TestCandlesMatchDLL`, `TestHistoryCollectsParts` | `feat(native): gethistorydata` | — |
| 14 | `subscribe_ticks` → `<ticks>` (RQ_SUBSCRIBE_TICKS 120, RQ_TICKS 13) | 3 | 3, 4 (выв.) | Create `native/session/ticks.go` + тест, `native/records/ticks.go` + тест, `native/model/tick.go`, `native/client_ticks.go` | RE-1; RE-2 (Wine есть); RE-4 (нужен TC_PASSWORD) | `TestTicksPayloadMatchesDLL`, `TestTicksMatchDLL` | `feat(native): subscribe_ticks` | — |
| 15 | Транзакции RQ_TRANSACT (14): номер, ответ, неопределённый исход | 4 | 2 (`model`), 3 (testserver) | Create `native/session/transact.go`, `native/session/transact_test.go`, `native/model/transaction.go` | RE-1; RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestNewOrderLostReplyIsUncertain` | `feat(session): RQ_TRANSACT with explicit unknown outcome` | **RF2** |
| 16 | `neworder`, `cancelorder` и поток заявок `<orders>` | 4 | 15 (`session.Transact`), 3, 4 | Create `native/records/order.go` + тест, `native/model/order.go`, `native/client_orders.go` + тест; Modify `native/xmlcompat/render.go`, `native/connector.go` | RE-1; RE-2 (Wine есть, с согласия); RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestNewOrderPayloadMatchesDLL`, `TestCancelOrderPayloadMatchesDLL`, `TestOrdersMatchDLL` | `feat(native): neworder, cancelorder and order updates` | — |
| 17 | Сделки клиента `<trades>` (R_TRD 0x04) | 4 | 3, 4 (выв.; от 16 не зависит) | Create `native/records/clienttrade.go` + тест, `native/model/clienttrade.go`; Modify `native/xmlcompat/render.go` | RE-1; RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestClientTradesMatchDLL` | `feat(native): client trades` | — |
| 18 | `newcondorder`, `newstoporder`, `cancelstoporder`, R_STOP (0x54) | 4 | 15, 16 (`model/order.go`), 3, 4 | Create `native/records/stoporder.go` + тест, `native/model/stoporder.go`, `native/client_stoporders.go`; Modify `native/model/order.go`, `native/xmlcompat/render.go` | RE-1; RE-2 (Wine есть, с согласия); RE-3 (Wine есть; нужна запись+TC_PASSWORD) | тесты «по образцу задачи 16» для трёх команд (имена в плане не заданы), `TestStopOrdersMatchDLL` | `feat(native): conditional and stop orders` | — |
| 19 | `moveorder` и `makeordown` | 4 | 15, 16 | Modify `native/client_orders.go` + тест, `native/session/transact.go` | RE-1; RE-2 (Wine есть) | `TestMoveOrderPayloadMatchesDLL` | `feat(native): moveorder` | — |
| 20 | Поток `<positions>`: деньги, бумаги, FORTS | 5 | 3, 4 (выв.) | Create `native/records/positions.go` + тест, `native/model/position.go`, `native/client_positions.go`; Modify `native/xmlcompat/render.go` | RE-1; RE-2 (Wine есть); RE-3 (Wine есть; нужна запись+TC_PASSWORD); RE-4 (нужен TC_PASSWORD) | `TestPositionsMatchDLL` | `feat(native): positions` | — |
| 21 | `get_forts_positions`, `get_client_limits` (RQ_CLIENT_LIMITS 43, RQ_FORTS_FIRM_MONEY 47) | 5 | 20 (`client_positions.go`), 3, 4 | Create `native/session/limits.go` + тест, `native/records/limits.go` + тест, `native/model/limits.go`; Modify `native/client_positions.go`, `native/xmlcompat/render.go`, `native/connector.go` | RE-1; RE-2 или RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestClientLimitsPayloadMatchesDLL`, `TestClientLimitsMatchDLL` | `feat(native): forts positions and client limits` | — |
| 22 | Портфели: `get_portfolio`, `get_portfolio_mct`, `get_mc_portfolio`, `get_united_portfolio`, `get_united_equity`, `get_united_go` | 5 | 3, 4 (выв.) | Create `native/session/portfolio.go` + тест, `native/records/portfolio.go` + тест, `native/model/portfolio.go`, `native/client_portfolio.go`; Modify `native/xmlcompat/render.go`, `native/connector.go` | RE-1; RE-2 (Wine есть); RE-4 (нужен TC_PASSWORD) | пары (`…PayloadMatchesDLL`, `…MatchesDLL`) на каждую команду; имена в плане не заданы | `feat(native): portfolios` | — |
| 23 | `get_max_buy_sell_tplus`, `get_max_buy_sell`, `get_cln_sec_permissions`, режим кредитования | 5 | 3, 4 (выв.; Consumes в плане не указаны) | Create `native/session/margin.go` + тест, `native/records/margin.go` + тест, `native/model/margin.go`, `native/client_margin.go` | RE-1; RE-2 (Wine есть) | пары на команду; имена не заданы | `feat(native): max buy/sell and security permissions` | — |
| 24 | Новости: `get_old_news`, `get_news_body`, поток `<news_header>` | 6 | 3, 4 (выв.) | Create `native/session/news.go` + тест, `native/records/news.go` + тест, `native/model/news.go`, `native/client_news.go` | RE-1; RE-2 (Wine есть) | `TestOldNewsPayloadMatchesDLL`, `TestNewsMatchDLL` | `feat(native): news` | — |
| 25 | Текстовые сообщения `<messages>` (R_MSG 0x01) | 6 | 3, 4 (выв.) | Create `native/records/message.go` + тест, `native/model/message.go` | RE-1; RE-3 (Wine есть; нужна запись+TC_PASSWORD) | `TestMessagesMatchDLL` | `feat(native): text messages` | — |
| 26 | `change_pass` (RQ_CHANGE_PWD 3) | 6 | 5 (ключ `recover`), 3, 4 | Create `native/session/changepwd.go` + тест, `native/client_password.go` | RE-1; RE-3 (Wine есть; живая смена пароля запрещена без согласия) | `TestChangePasswordPayloadMatchesDLL` | `feat(native): change_pass` | — |
| 27 | `get_option_families`, `get_family_strikes`, `get_options` | 7 | 3, 4 (выв.) | Create `native/session/options.go` + тест, `native/records/options.go` + тест, `native/model/option.go`, `native/client_options.go` | RE-1; RE-2 (Wine есть) | пары на команду; имена не заданы | `feat(native): options` | — |
| 28 | `newrpsorder`, `newrepoorder`, `newmrepoorder`, `cancelnegdeal`, `newreport4`, `cancelreport4`; сделки и отчёты | 8 | 15 (`session.Transact`), 3, 4 | Create `native/records/rps.go` + тест, `native/model/rps.go`, `native/client_rps.go` | RE-1; RE-3 (Wine есть; нужна запись+TC_PASSWORD) | пары на команду; имена не заданы | `feat(native): RPS and REPO` | — |
| 29 | XML-коннектор целиком и gRPC-сервер без Wine | 9 | 1 (RE-4 через `tools/tcproxy`), 5–28 (все методы `Client` + `Render`) | Modify `native/connector.go`, `server/server.go`, `docs/protocol/commands.md`; Create `native/connector_matrix_test.go`, `docs/protocol/errors.md`, тест(ы) в `server/` | RE-1 (`set_csp`, `authenticate`); RE-3 (Wine есть, тексты ошибок); RE-4 (нужен TC_PASSWORD) | `TestEveryDLLCommandIsHandled`, `TestServerErrorTexts`, `TestServerRunsNativeBackend` | `feat(server): native backend without Wine` | — |
| 30 | Матрица покрытия, длительный прогон, выпуск | 9 | 29 (фактически все) | Create `docs/protocol/coverage.md`, `tools/native-soak/main.go`; Modify `docs/ROADMAP-GO.md`, `README.md` | — (soak использует `tools/tcproxy` из задачи 1; критерии: `UnknownRecords==0`, восстановления, RSS) | именованных тестов нет; критерии прогона в шаге 2 | `docs: native client coverage matrix and soak results` | — |

---

## 5. Общие файлы — узкие места

Файлы/каталоги, в которые пишут несколько задач. Пока такие задачи не разведены по разным файлам,
их **нельзя вести параллельно** (совпадение write-scope — предупреждение, а не блокировка; конфликт
будет при merge).

| Файл / каталог | Кто пишет (задачи плана) | Характер записи | Как сериализовать |
|---|---|---|---|
| `native/xmlcompat/render.go` | 4 (создаёт), 6, 7, 8, 9, 10, 11, 13, 16, 17, 18, 20, 21, 22 | Каждая задача добавляет `case` в `Render` | Только по одной карточке одновременно; порядок — по фазам плана. Альтернатива (в плане отсутствует): в задаче 4 разнести dispatch по файлам `render_<record>.go` — тогда часть задач распараллелится; требует согласования со Lead |
| `native/connector.go` | 2 (импорты), 3, 5, 6, 7, 9, 11, 13, 16, 21, 22, 29 | Таблица команд/рендер/статусы | Сериализовать; таблицу команд для всех 45 имён собирает задача 29 |
| `native/client.go` | 2, 3, 5, 9 | API клиента, подписки, `Run`/`Poll`, `subs` | Сериализовать (2 → 3 → 5 → 9) |
| `native/events.go` | 3 (создаёт реестр `registerDecoder`), 8 (меняет) | Реестр декодеров RQ_MAIN | **Точка не определена планом:** задачи 9–28 добавляют декодеры, но файл регистрации в их Files не указан. Пока не решено в задаче 3 (регистрация в собственном файле задачи или отдельный файл на запись), считать `native/events.go` общим для всех декодеров |
| `native/xmlcompat/testdata/` → `native/records/testdata/` | 2 (переносит), 3, 4 (читают в тестах), далее все задачи кладут фикстуры | Фикстуры записей | Задача 2 переносит каталог; до её закрытия никто не добавляет фикстуры. Новые фикстуры — только `native/records/testdata/` |
| `native/model/order.go` | 16 (создаёт), 18 (условия `newcondorder`) | Один тип заявки | 16 → 18 |
| `native/model/secinfo.go` | 8 (создаёт), 11 (дополняет `SecInfo`) | Типы sec_info | 8 → 11 |
| `native/session/transact.go` | 15 (создаёт), 19 (добавляет `moveorder`/`makeordown`) | Транзакции | 15 → 19 |
| `native/client_subscribe.go` | 9 (создаёт), 10 (расширяет `Subscription`) | Подписки | 9 → 10 |
| `native/client_positions.go` | 20 (создаёт), 21 (добавляет лимиты) | Позиции/лимиты | 20 → 21 |
| `native/client_orders.go` | 16 (создаёт), 19 (добавляет `MoveOrder`) | Заявки | 16 → 19 |
| `native/internal/testserver/testserver.go` | 3 (создаёт, выносит `fakeServer`), 5 (`Drop()` — Consumes) | Тестовый сервер | 3 → 5; см. §8 п.2/п.15 о тестовом API и `internal` |
| `docs/protocol/connect.md` | 5, 6 | Формат RQ_RELOGIN/RQ_RECOVER, параметры `connect` | 5 → 6 |
| `docs/protocol/commands.md` | 6, 29 | Ответы команд, тексты ошибок | 6 → 29 |
| `docs/protocol/coverage.md`, `docs/ROADMAP-GO.md`, `README.md` | 30 | Итоговые документы | Только задача 30 |
| `tools/re/README.md` | 1 | Go-варианты RE-2/RE-3 | Только задача 1 |
| `server/server.go` | 29 | `Backend`, `TC_BACKEND` | Только задача 29 |
| `native/session/login.go` (константы RQ_*) | фактически задачи 11, 13, 14, 15, 16, 17, 21, 22, 23, 24, 26, 28 | Новые константы типов запросов | Планом не назначено: либо задача 6/11 заводит отдельный файл констант, либо `login.go` становится общим — см. §8 п.16 |

---

## 6. Параллельные группы

Группы учитывают: (а) зависимости Interfaces/Consumes, (б) пересечение write-scope из §5,
(в) порядок фаз. Внутри группы карточки можно запускать одновременно; между группами — только после
закрытия `blocked_by`.

### G0. Фундамент (обязателен первым; без него остальные фазы не начинаются)

| Шаг | Задачи | Параллельность |
|---|---|---|
| G0a | 1 и 2 | План не связывает их через Interfaces, но внутри фазы предписан порядок и у них общий каталог фикстур: тест задачи 1 читает `native/xmlcompat/testdata/{startup-demo.hex,main1-demo.hex.gz,alltrades-inject1.hex}` и `native/session/login_test.go`, а задача 2 переносит `testdata/*` в `native/records/testdata`. Безопаснее: **1, затем 2**; при параллельном запуске задача 2 не трогает `testdata` до закрытия задачи 1 (см. §8 п.4) |
| G0b | 3 | После 2 (и закрытия задачи 1, если выбрана последовательность 1→2) |
| G0c | 4 | После 2 и 3 |

### G1. Первая параллельная волна после фундамента

`{5, 12, 14, 15}` — 4 карточки одновременно. Write-scope не пересекаются (оговорки ниже):
- 5 — `native/session/recover.go`, `native/recover.go`, `native/client.go`, `native/connector.go`,
  `native/internal/testserver/testserver.go`, `docs/protocol/connect.md`;
- 12 — `native/records/misc.go`, `native/model/misc.go`;
- 14 — `native/session/ticks.go`, `native/records/ticks.go`, `native/model/tick.go`, `native/client_ticks.go`;
- 15 — `native/session/transact.go`, `native/model/transaction.go`.

Оговорки: (1) 5 — единственный писатель `connector.go` в этой волне, поэтому полоса G2 (см. ниже)
стартует после неё; (2) 12, 14, 15 добавляют декодеры/события — если регистрация окажется в
`native/events.go`, задачи 12/14 пересекутся с 8 (см. §8 п.9).

### G2. Полоса `render.go`/`connector.go` — строго последовательно

`{6, 7, 8, 9, 10, 11, 13, 16, 17, 18, 20, 21, 22}` — **только по одной карточке одновременно**.
Внутренние зависимости: 5 → 6 (общий `connect.md`); 6 → 7 (общие `render.go`/`connector.go`); 9 → 10; 8 → 11; 15 → 16; 16 → 18; 20 → 21.
Задачи 13, 17, 22 не имеют внутренних зависимостей и встают в любое окно полосы.

### G3. Параллельно полосе G2 (не пишут `render.go`/`connector.go`)

Карточки, которые можно вести одновременно с G2, как только закрыты их `blocked_by`:

| Задача | blocked_by | Оговорка |
|---|---|---|
| 19 | 16 | Пишет `client_orders.go` и `session/transact.go`; с задачами 17/18/20 не пересекается |
| 23 | 4 | План не перечисляет Consumes; write-scope изолирован |
| 24 | 4 | **Проверить:** `<news_header>` — XML-форма, `render.go` в Files не указан (§5; §8 п.8–9); если формы нет — параллельно, если есть — в G2 |
| 25 | 4 | **Проверить:** `<messages>` — XML-форма, `render.go` в Files не указан (§5; §8 п.8–9) |
| 26 | 5 | Изолирован (`session/changepwd.go`, `client_password.go`) |
| 27 | 4 | **Проверить:** XML-формы опционов, `render.go`/`connector.go` в Files не указаны (§5; §8 п.8–9) |
| 28 | 15 | **Проверить:** события `RPSTradeForExecution`/`ExecutionReport` требуют XML-формы (§5; §8 п.8–9); зависит от `session.Transact` |

### G4. Интеграция

| Шаг | Задачи | blocked_by |
|---|---|---|
| G4a | 29 | Все 5–28 (методы `Client` и `Render`), 1 (`tools/tcproxy` для RE-4) |
| G4b | 30 | 29 |

### Черновики `team_task_create`

`T#` — id карточки задачи # (подставить фактические id при создании; `blocked_by` принимает id
карточек, не номера плана).

**G0a**

- `subject`: `Task 1: Прокси и проигрыватель на Go`
  `write_scopes`: `["tools/tcproxy/", "native/internal/replay/", "tools/replay-server/", "tools/re/README.md"]`
  `blocked_by`: `[]`
  Кратко: перенести `py/proxy.py` и `py/replay_server.py` на Go поверх `native/wire`; формат захвата
  `"<conn> <sec> C>S|S>C <len> <hex>"`; `replay.Session{Load,Serve}`, `Injection`.
- `subject`: `Task 2: Пакеты native/model и native/records`
  `write_scopes`: `["native/model/", "native/records/", "native/xmlcompat/", "native/client.go", "native/connector.go", "native/natsout/natsout.go", "tools/"]`
  `blocked_by`: `[]`
  Кратко: `model.Decimal`/`Symbol`/`Trade`/`Security`/`Board`/`Market`/`CandleKind`; перенос разбора
  записей и каталога из `xmlcompat` в `records`; `xmlcompat` — только XML. Не запускать одновременно
  с T1, если T1 держит `native/xmlcompat/testdata` (см. §8 п.4).

**G0b**

- `subject`: `Task 3: Событийный API Client.Run`
  `write_scopes`: `["native/model/event.go", "native/events.go", "native/internal/testserver/", "native/client.go", "native/client_test.go", "native/connector.go", "native/connector_test.go", "tools/native-trades/main.go"]`
  `blocked_by`: `[T2]`
  Кратко: `model.Event`/`UnknownRecord`, `Poll`/`Run`/`RunOptions`, реестр `registerDecoder`,
  `testserver.Server` (`Snapshot`, `AfterSubscribe`, `MainDelay`, `Handle`, `Requests`); перевести
  `R_ALLTRADE` на реестр, `Connector` и `native-trades` — на `Run`.

**G0c**

- `subject`: `Task 4: Шаблон задачи «семейство» и сверка XML`
  `write_scopes`: `["native/records/oracle_test.go", "native/xmlcompat/render.go"]`
  `blocked_by`: `[T2, T3]`
  Кратко: `records.LoadFixture`, `xmlcompat.Render`, шаблон `checkDLL`; case `*model.Trade` на
  существующем `AllTradesXML`. **Проверить** размещение помощника: `_test.go` в `records` нельзя
  импортировать из других пакетов (§8 п.3).

**G1**

- `subject`: `Task 5: Переподключение и восстановление (RQ_RELOGIN 63, RQ_RECOVER 64)`
  `write_scopes`: `["native/session/recover.go", "native/session/recover_test.go", "native/recover.go", "native/recover_test.go", "native/client.go", "native/connector.go", "native/internal/testserver/testserver.go", "docs/protocol/connect.md"]`
  `blocked_by`: `[T3, T4]`
  Кратко: RE-1 по `FUN_1803d31c0`/`FUN_1803d27b0`; RE-2 E05 (Wine есть; нужен `TC_PASSWORD` — фикстуры из захвата
  или `blocked`); `session.RecoverPayload`, `Client.recover`, `model.ServerStatus`.
- `subject`: `Task 12: Остальные записи снапшота и обновлений справочников`
  `write_scopes`: `["native/records/misc.go", "native/records/misc_test.go", "native/model/misc.go"]`
  `blocked_by`: `[T4]`
  Кратко: R_DOUBLELEG 0x66, R_BOARD 0x34, 0x38, 0x43, R_CURRENCY_RATE 0x42, R_FORTS_SESSION 0x6b,
  R_EDP_RATE 0x6e, R_DELAY 0x4f, R_OPT_EXPIRATION 0x39, 0x5d, 0x6f–0x7c; RE-1 + RE-3 (Wine есть; нужна запись+TC_PASSWORD).
- `subject`: `Task 14: subscribe_ticks → <ticks> (RQ_SUBSCRIBE_TICKS 120, RQ_TICKS 13)`
  `write_scopes`: `["native/session/ticks.go", "native/session/ticks_test.go", "native/records/ticks.go", "native/records/ticks_test.go", "native/model/tick.go", "native/client_ticks.go"]`
  `blocked_by`: `[T4]`
  Кратко: RE-1, RE-2 (Wine есть); `TickRequest`/`SubscribeTicks`, событие `model.Tick`.
- `subject`: `Task 15: Транзакции RQ_TRANSACT (14): номер, ответ, неопределённый исход`
  `write_scopes`: `["native/session/transact.go", "native/session/transact_test.go", "native/model/transaction.go"]`
  `blocked_by`: `[T2, T3]`
  Кратко: `TxKind`, `session.Transact`, `ErrOutcomeUnknown`/`OutcomeError`, `TransactionsInProgress`;
  RE-1 + RE-3 (Wine есть; нужна запись+TC_PASSWORD). Review Focus 2.

**G2 (полоса; по одной карточке)**

- `subject`: `Task 6: Служебные команды и параметры connect`; `write_scopes`:
  `["native/client_service.go", "native/client_service_test.go", "native/connector.go", "native/xmlcompat/render.go", "docs/protocol/commands.md", "docs/protocol/connect.md"]`;
  `blocked_by`: `[T4, T5]`; RE-1/RE-2 (Wine есть)/RE-3 (Wine есть; нужна запись+TC_PASSWORD).
- `subject`: `Task 7: Сообщения конца загрузки: <client>, <positions>, <overnight>, <union>`;
  `write_scopes`: `["native/records/client.go", "native/records/client_test.go", "native/model/client.go", "native/client_accounts.go", "native/xmlcompat/render.go", "native/connector.go"]`;
  `blocked_by`: `[T4, T6]`; **проверить** зависимость от задачи 20 (§8 п.10).
- `subject`: `Task 8: <sec_info_upd> из R_SEC_PARAM (0x5e)`; `write_scopes`:
  `["native/records/secparam.go", "native/records/secparam_test.go", "native/model/secinfo.go", "native/xmlcompat/render.go", "native/events.go"]`;
  `blocked_by`: `[T4]`; RE не нужен, но фикстур #44 в репозитории нет (§8 п.11).
- `subject`: `Task 9: <quotations> — подписка (маска 0x01) и запись 0x06`; `write_scopes`:
  `["native/records/quotation.go", "native/records/quotation_test.go", "native/model/quotation.go", "native/client_subscribe.go", "native/client.go", "native/connector.go", "native/xmlcompat/render.go"]`;
  `blocked_by`: `[T4]`; Review Focus 4; RE-1/2/3/4 (нет).
- `subject`: `Task 10: <quotes> — стакан (маска 0x02) и запись 0x07`; `write_scopes`:
  `["native/records/quote.go", "native/records/quote_test.go", "native/model/quote.go", "native/client_subscribe.go", "native/xmlcompat/render.go"]`;
  `blocked_by`: `[T9]`.
- `subject`: `Task 11: get_securities_info → <sec_info> (RQ_SEC_SPEC 15, RQ_INQUIRE_SECURITY 111)`;
  `write_scopes`: `["native/session/secspec.go", "native/session/secspec_test.go", "native/records/secinfo.go", "native/records/secinfo_test.go", "native/client_secinfo.go", "native/model/secinfo.go", "native/xmlcompat/render.go", "native/connector.go"]`;
  `blocked_by`: `[T8]`.
- `subject`: `Task 13: gethistorydata → <candles> (RQ_HISTORY 11, RQ_HISTORY_EXT 148)`;
  `write_scopes`: `["native/session/history.go", "native/session/history_test.go", "native/records/candles.go", "native/records/candles_test.go", "native/model/candle.go", "native/client_history.go", "native/client_history_test.go", "native/xmlcompat/render.go", "native/connector.go"]`;
  `blocked_by`: `[T4]`.
- `subject`: `Task 16: neworder, cancelorder и поток заявок <orders>`; `write_scopes`:
  `["native/records/order.go", "native/records/order_test.go", "native/model/order.go", "native/client_orders.go", "native/client_orders_test.go", "native/xmlcompat/render.go", "native/connector.go"]`;
  `blocked_by`: `[T15]` (+ общий `render.go`/`connector.go`).
- `subject`: `Task 17: Сделки клиента <trades> (R_TRD 0x04)`; `write_scopes`:
  `["native/records/clienttrade.go", "native/records/clienttrade_test.go", "native/model/clienttrade.go", "native/xmlcompat/render.go"]`;
  `blocked_by`: `[T4]` (+ полоса `render.go`).
- `subject`: `Task 18: newcondorder, newstoporder, cancelstoporder, R_STOP (0x54)`;
  `write_scopes`: `["native/records/stoporder.go", "native/records/stoporder_test.go", "native/model/stoporder.go", "native/client_stoporders.go", "native/model/order.go", "native/xmlcompat/render.go"]`;
  `blocked_by`: `[T15, T16]`.
- `subject`: `Task 20: Поток <positions>: деньги, бумаги, FORTS`; `write_scopes`:
  `["native/records/positions.go", "native/records/positions_test.go", "native/model/position.go", "native/client_positions.go", "native/xmlcompat/render.go"]`;
  `blocked_by`: `[T4]`.
- `subject`: `Task 21: get_forts_positions, get_client_limits`; `write_scopes`:
  `["native/session/limits.go", "native/session/limits_test.go", "native/records/limits.go", "native/records/limits_test.go", "native/model/limits.go", "native/client_positions.go", "native/xmlcompat/render.go", "native/connector.go"]`;
  `blocked_by`: `[T20]`.
- `subject`: `Task 22: Портфели`; `write_scopes`:
  `["native/session/portfolio.go", "native/session/portfolio_test.go", "native/records/portfolio.go", "native/records/portfolio_test.go", "native/model/portfolio.go", "native/client_portfolio.go", "native/xmlcompat/render.go", "native/connector.go"]`;
  `blocked_by`: `[T4]` (+ полоса).

**G3 (параллельно полосе G2)**

- `Task 19`: `["native/client_orders.go", "native/client_orders_test.go", "native/session/transact.go"]`,
  `blocked_by: [T16]`.
- `Task 23`: `["native/session/margin.go", "native/session/margin_test.go", "native/records/margin.go", "native/records/margin_test.go", "native/model/margin.go", "native/client_margin.go"]`,
  `blocked_by: [T4]`.
- `Task 24`: `["native/session/news.go", "native/session/news_test.go", "native/records/news.go", "native/records/news_test.go", "native/model/news.go", "native/client_news.go"]`,
  `blocked_by: [T4]`; проверить `render.go`/регистрацию (§5; §8 п.8–9).
- `Task 25`: `["native/records/message.go", "native/records/message_test.go", "native/model/message.go"]`,
  `blocked_by: [T4]`; проверить `render.go`/регистрацию R_MSG.
- `Task 26`: `["native/session/changepwd.go", "native/session/changepwd_test.go", "native/client_password.go"]`,
  `blocked_by: [T5]`.
- `Task 27`: `["native/session/options.go", "native/session/options_test.go", "native/records/options.go", "native/records/options_test.go", "native/model/option.go", "native/client_options.go"]`,
  `blocked_by: [T4]`; проверить `render.go`/`connector.go`.
- `Task 28`: `["native/records/rps.go", "native/records/rps_test.go", "native/model/rps.go", "native/client_rps.go"]`,
  `blocked_by: [T15]`; проверить XML-формы и `connector.go`.

**G4 (интеграция)**

- `Task 29`: `["native/connector.go", "server/server.go", "native/connector_matrix_test.go", "server/", "docs/protocol/commands.md", "docs/protocol/errors.md"]`,
  `blocked_by: [T1, T5–T28]`; RE-1/RE-3 (Wine есть; нужна запись+TC_PASSWORD)/RE-4 (нужен TC_PASSWORD).
- `Task 30`: `["docs/protocol/coverage.md", "tools/native-soak/main.go", "docs/ROADMAP-GO.md", "README.md"]`,
  `blocked_by: [T29]`.

---

## 7. Задачи, которые нельзя распараллелить вообще

1. **Цепочка фундамента:** `2 → 3 → 4` — строго последовательно (3 `Consumes records`, 4 использует
   `model.Event` и `records.Catalog`). Задача 1 формально независима, но делит с задачей 2 каталог
   фикстур (см. §8 п.4) — безопасный вариант `1 → 2 → 3 → 4`.
2. **Полоса `render.go`/`connector.go`:** 13 задач (6, 7, 8, 9, 10, 11, 13, 16, 17, 18, 20, 21, 22)
   не могут выполняться одновременно ни между собой, ни с задачами 3, 5, 29, которые тоже пишут
   `connector.go`. Это фактически сериализует фазы 2–8 плана, хотя план (строка 38) утверждает, что
   фазы 1–8 зависимы между собой только по Interfaces.
3. **Интеграция:** 29 → 30.
4. Итог: **ни одна задача плана не может идти одновременно с другой, если обе пишут в
   `native/xmlcompat/render.go` или `native/connector.go`** (14 задач), а также 2→3→4 и 29→30.
   Остальные пары из §6 распараллеливаются только после решения вопроса с `native/events.go`
   (регистрация декодеров).

---

## 8. Противоречия и пробелы

Проверено по фактическому дереву на `main` = `b940f9a` и по тексту плана. Спорное помечено
«проверить». Ничего не додумано: где данных нет — так и написано.

1. **45 имён команд против 43 в коде.** План (карта команд, строка 57) перечисляет **45** имён и
   задача 29 требует «для каждого из 45 имён `dllCommands`». Фактически `dllCommands` в
   `native/connector.go` содержит **43** имени; отсутствуют `newreport4` и `cancelreport4` (оба
   упомянуты в карте плана и в задаче 28). `docs/protocol/commands.md` тоже пишет «43 имени».
   → Задаче 28/29 нужно добавить обе команды в `dllCommands`, а расхождение 43/45 — снять в
   `docs/protocol/commands.md`.
2. **`native/internal/testserver` не сможет использовать `server/`.** Задача 3 создаёт
   `native/internal/testserver` «чтобы его могли использовать тесты всех пакетов», но пакеты внутри
   `native/internal/...` импортируемы только из поддерева `native/`. Задача 29 требует
   `TestServerRunsNativeBackend` в `server/` с testserver → из `server/` этот пакет импортировать
   нельзя. → Решение на задачу 3: либо `native/testserver` (не `internal`), либо тестовая копия в
   `server/`.
3. **`checkDLL` в `native/records/oracle_test.go`.** `_test.go`-файлы не экспортируются в другие
   пакеты, а `checkDLL` нужен тестам `native/xmlcompat` (задачи 6–22) и, возможно, другим. План
   называет помощника «тестовый шаблон», но не решает, где он живёт. → Задача 4 должна определить
   место, например `native/internal/oracle` (но см. п.2 об `internal`) или экспортируемый
   `native/records/oracle.go` с build-тегом `//go:build !prod` — **проверить/согласовать с Lead**.
4. **Фикстуры задачи 1 против переноса в задаче 2.** Задача 1 (тест `TestReplayServesRecordedSession`)
   читает `startup-demo.hex`, `main1-demo.hex.gz`, `alltrades-inject1.hex` (сейчас —
   `native/xmlcompat/testdata/`) и `stubPwdReply` из `native/session/login_test.go`. Задача 2
   переносит `native/xmlcompat/testdata/*` в `native/records/testdata/`, но `native/internal/replay`
   в Files задачи 2 не указан. → Либо 1 строго до 2 и задача 2 правит пути теста задачи 1, либо
   задача 1 читает фикстуры через путь-константу.
5. **«Move» смешанных файлов в задаче 2.** `native/xmlcompat/security.go` содержит не только
   `ParseSecNew`/`SecNew`/`Pit`, но и XML-конверсию (`Security`, `boardCode`, `FormatMinStep`,
   `FormatPointCost`) и импортирует `client/commands`; `startup.go` импортирует `client/commands` и
   `golang.org/x/text/encoding/charmap`; `catalog.go` зависит от типов `startup.go`/`security.go`. План говорит «Move … в
   `records`» и одновременно «`xmlcompat` остаётся только для XML». → Это не перенос, а разделение
   файлов; задача 2 должна быть сформулирована как «split», иначе `records` начнёт зависеть от
   `client/commands` и XML-структур.
6. **`native/model/venue`-типы.** `model.Board`, `model.Market`, `model.CandleKind` план создаёт в
   `native/model/dictionary.go`, но сейчас эти типы живут в `xmlcompat/startup.go` с XML-тегами и
   используются `commands.*`. Нужно развести модель и XML-представление — план этого не описывает.
   **Проверить** в задаче 2.
7. **`docs/protocol/orders.md`.** Задача 19 ссылается на `docs/protocol/orders.md`, но файла нет, и
   ни одна задача плана его не создаёт (в отличие от `errors.md`/`coverage.md`, которые создаются
   задачами 29/30). → Добавить создание в задачу 19 (или заменить ссылку на существующий документ).
8. **Документация после каждой фазы.** «Как устроен план» требует «документация обновлена» после
   каждой фазы, но `docs/protocol/*.md` в секции Files есть только у задач 5 и 6 (у задач 29–30 —
   `errors.md`/`coverage.md` из шагов; `tools/re/README.md` — у задачи 1); задача 8 ссылается на `dictionaries.md`, но не перечисляет её в Files; задачи 9–28
   не указывают, где фиксируются статусы S/O/D/I для новых записей. → Пробел: назначить файл
   документации в каждой задаче (или отдельную карточку RE-документации на фазу).
9. **Точка регистрации декодеров.** План: «декодеры регистрируются через `registerDecoder` в
   `native/events.go` — так задачи 6–28 добавляют записи, не трогая цикл опроса». Но `events.go`
   указан в Files только у задач 3 и 8; у остальных (9–28) файл регистрации не указан. Если каждая
   задача правит `native/events.go`, это второй по важности общий файл после `render.go`.
   → Задача 3 должна задать конвенцию (регистрация в собственном `native/client_<...>.go` или
   отдельный файл на запись). **Проверить.**
10. **`<positions>` в задаче 7 требует типов задачи 20.** Шаг 4 задачи 7 требует порядка колбэков
    `Connector`: `<client>`, `<positions>`, `<overnight>` перед `server_status`, но типы позиций
    (`model.SecPosition` и т. п.) создаются в задаче 20 (фаза 5). Зависимость через фазы в плане не
    объявлена. → Либо задача 7 отдаёт `<positions>` в виде уже существующих `client/commands`
    структур, либо её шаг с `<positions>` переносится/блокируется задачей 20. **Проверить.**
11. **Отсутствующие фикстуры, названные в задачах** — частично закрыто в Фазах 1–2 (2026-11-04).
    Созданы и закоммичены: `recover-{short,long}-live.*` (задача 5), `service-dll.xml`,
    `connect-options-live.*` (задача 6), `snapshot-tail-live.*` (задача 7), `secparam-live.hex.gz` +
    `sec_info_upd-dll.xml.gz` (задача 8), `quotation-live.hex` + `quotations-dll.xml` (9),
    `quote-live.hex` + `quotes-dll.xml` (10), `secinfo-live.*` + `secinfo-dll.xml.gz` + `secspec-request.hex`
    (11), `candles-live.*` + `candles-part{1,2}-inject.hex` + `candles-multipart-dll.xml` (13),
    `transact-{success,error,empty}-reply.hex`/`-dll.xml` (15).\n    Остаются вне git/не сняты: `ticks-live` (после основной сессии 5 ноября или oracle reply-инъекции —
    task-23/31), фикстуры `neworder/cancel/stop/cond`-ответов и записей заявок/сделок (RE task-32 в работе),
    позиции/лимиты/портфели (задачи 20–22), а также «полный снапшот» >2 МБ (в git не кладём; env-gated тесты).
12. **Задача 8 «может оказаться не сверяемой побайтно» — РЕШЕНО с ограничением (§8.12).** Форма/
    значения теперь **S/D**: рендер 200/200 побайтно против `sec_info_upd-dll.xml.gz`, полная
    независимая сверка по локальному E07-захвату — 684/684. Ограничение: окно фикстуры (secid 15 000–15 599)
    отсутствует в `main1-demo.hex.gz` (`R_SEC_NEW` для него нет), поэтому `seccode`/`market`/`point_cost`
    тест сеет из ожидаемых XML-строк, а `Kind`/`Pits` — нет (форма выбирается fallback-битом 0x4).
    Полная независимость потребует R_SEC_NEW-фикстуры окна (отдельная RE-карточка, не блокер). Исправлено
    в задаче 8 (commits c616db1, 6a6e1e7, b05c648), статусы — `docs/protocol/dictionaries.md`.
13. **Числа из захватов в задачах 6, 11.** Задача 6 ссылается на «два живых логина DLL» и
    `native/testdata/service-dll.xml`, задача 11 — на payload `00040379017b001a` и «273 байта на
    4 бумаги»; этих материалов в репозитории нет. → Источник — захват пользователя вне git;
    воспроизводимость сейчас не гарантирована. **Проверить.**
14. **`RQ_SEC_SPEC` для Go.** `docs/protocol/alltrades.md` пишет: «После подписки DLL дополнительно
    шлёт `RQ_SEC_SPEC` (15) … Go-клиенту он не нужен». Задача 11, наоборот, реализует
    `get_securities_info` через RQ_SEC_SPEC (15) и RQ_INQUIRE_SECURITY (111). Это не обязательно
    противоречие (нужен не для подписки, а для команды), но формулировку в `alltrades.md` надо
    уточнить. **Проверить.**
15. **`fakeServer` ≠ `testserver.Server`.** Задача 3 говорит «вынести `fakeServer`», но у текущего
    `fakeServer` (`native/connector_test.go`) нет `Handle`, `Requests`, `AfterSubscribe`; есть
    `snapshot`, `trades`, `mainDelay`, `subscribes [][]byte`, `pending`, неэкспортированный `drop()`.
    → Задача 3 — это новый API, а не перенос; `Drop()` (задача 5) должен быть экспортированным.
16. **Константы типов запросов.** План вводит RQ_HISTORY (11), RQ_TICKS (13), RQ_TRANSACT (14),
    RQ_SEC_SPEC (15), RQ_DEPO_POS (16), RQ_MONEY_POS (17), RQ_REPORT_INFO (21), RQ_GET_REPORT (22),
    RQ_CLIENT_HOLDINGS (42), RQ_CLIENT_LIMITS (43), RQ_FORTS_FIRM_MONEY (47), RQ_POSITIONS (81),
    RQ_MARGIN_POS (84/85), RQ_FOND_PORTFOLIO (86), RQ_INQUIRE_UT (152), RQ_RPSREPO_SUBSCR (72),
    RQ_GET_RPSREPO (82), RQ_GETNEWS (73), RQ_CHANGE_PWD (3) и др. В `native/session` сейчас 11
    констант (в `login.go`), остальных нет. Ни одна задача не указывает файл, куда их добавлять;
    `login.go`/`requests.go` станут общими файлами. → Назначить файл констант в задаче 3 или 6.
    **Проверить.**
17. **RE-2/RE-3 доступны, но упираются в секреты и записанные сессии.** Wine 11.21 установлен и
    проверен (`run_dll.sh` + `get_connector_version`), поэтому RE-2 (живые шаги задач 5, 6, 7, 9, 10,
    11, 13, 14, 16, 18, 19, 20, 21, 22, 23, 24, 27) и RE-3 (6, 9, 10, 12, 15, 16, 17, 18, 25, 26, 28,
    29) технически возможны. Для них нужны `TC_PASSWORD` в `~/re/demo.env` и/или записанная сессия;
    без этого шаг остаётся `blocked` с причиной. Оракул догадками не подменять; живые торговые
    команды — только с согласия пользователя.
18. **Служебные документы команды.** `docs/deepseek/environment.md`, `docs/deepseek/README.md` и
    `tools/re/check_env.sh` подготовлены (task-1/Lead) и являются источником статуса окружения;
    индекс ссылается на них. При изменении окружения обновлять `environment.md` и сверяться с
    `tools/re/check_env.sh`, а не с этим индексом.
19. **README уже содержит раздел «Native Go client», но о другом.** Задача 30 требует «раздел
    «Нативный клиент» с примером `Dial`/`Subscribe`/`Run`»; в README есть `## Native Go client` про
    gRPC-клиент и `make client`. → Задаче 30 нужно не добавить раздел, а переписать/дополнить
    существующий, не потеряв документацию по gRPC.
20. **`go.mod`/Tech Stack.** План: Go 1.27.2, `golang.org/x/text` (CP1251), protobuf/NATS только в
    `natsout`/`tools` — совпадает с `go.mod` и текущим деревом; новых зависимостей не требуется.
    Расхождений нет, проверено.

---

## 9. Проверки (Definition of Done) для любой задачи

Из Global Constraints плана, `SKILL.md` §8–§9 и `AGENTS.md` §7/§9:

```bash
gofmt -l .                       # пусто
go vet ./...
go build ./...
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build ./native/...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./native/...
go test -race ./...
make verify                      # цель есть в Makefile: gofmt/vet/build/кроссы/race
```

В песочнице DSH (`workspace-write`) глобальный кэш Go недоступен: перед ручными `go build`/`go vet`/
`go test` нужен `export GOCACHE="$PWD/.cache/go-build"` (см. AGENTS.md §7; `make verify` подставляет
его сам).

Дополнительно по профилю: `make server_build` и `make queues_build` (Windows, `CGO_ENABLED=0`);
`make compile` — только при изменении `proto/`. Живые прогоны (`TXML_DEMO_*`, `TC_*`) — только под
env-переменными и `t.Skip` без них; тесты не ходят в сеть и не требуют DLL/Wine. Секреты и обезличенные
фикстуры (`TESTCLIENT`/`TESTUNION`/`TESTLOGIN`) — по AGENTS.md §6. Коммит — с сообщением из таблицы §4.

---

## 10. Источники и чем проверено

| Утверждение | Чем проверено |
|---|---|
| Список/содержание 30 задач, фазы, Review Focus, тесты, commit | `read` плана целиком (636 строк) |
| Процедуры RE-1…RE-4, состав `tools/re` | `read tools/re/README.md` (187 строк) |
| Правила Go и DoD | `read .kimi-code/skills/go-engineer/SKILL.md` (209 строк) |
| Этапы M1–M7, что уже закрыто | `read docs/ROADMAP-GO.md` (171 строка) |
| Что восстановлено по протоколу | `read docs/protocol/{connect,commands,dictionaries,alltrades}.md` |
| Наличие файлов/пакетов/типов | `git ls-files native tools docs`, `ls native/*/`, `ls tools`, `grep` по символам (`FormatPrice`, `TradeDedup`, `Catalog.Apply`, `stubPwdReply`, `dllCommands`, `fakeServer`, `ClientConfig`, `Connect`, `Config`) |
| 43 против 45 имён `dllCommands` | подсчёт по `native/connector.go` и строке 57 плана (Python-regex) |
| Отсутствие `native/model`, `native/records`, `native/internal`, `tools/tcproxy`, `tools/replay-server`, `tools/native-soak`, `docs/protocol/errors.md`, `docs/protocol/coverage.md`, `docs/protocol/orders.md` | `test -e` по полному списку путей из секций Files плана |
| Отсутствие фикстур `secparam-*`, `snapshot-tail-*`, `service-dll.xml` и каталогов `native/records/testdata`, `native/session/testdata`, `native/testdata` | `ls`/`test -e`; сверено с `docs/protocol/dictionaries.md` |
| Ссылки плана, которые **существуют** (проверены): `native/xmlcompat/testdata/alltrades-inject{1,2,3}.hex` и `alltrades-dll{1,2,3}.xml`, `native/session/login_test.go` (`stubPwdReply`, `SessionKeys`), `native/wire` (`DefaultKeys`), `native.Dial`, `tools/{native-trades,reference-harness,wire-decode}`, `docs/protocol/{connect,dictionaries,commands,alltrades}.md`, `client/commands.Connect` | `git ls-files`, `ls`, `grep` по символам |
| Доступность RE | по AGENTS.md §4 и ограничениям текущей среды (Wine/демо-доступ отсутствуют) |

---

## 11. Фаза 0: итог и решения Lead (2026-11-04)

Ветка `plan/native-client` (от `main` = `488e7b7`); карточки `task-1`…`task-4` закрыты. Коммиты:
`e7ebc10` (T1), `2c22bf5` (T2), `acd84e2` (T3), `a2a4c85` (T4); фиксы по ревью второй моделью —
`79adb3f` (T1 P2), `3a46898` (T2 P2), `7e3f378` (T3 P1), плюс `153033a` (docs-хвост T2).

Что появилось: `tools/tcproxy`, `native/replay`, `tools/replay-server` (Go-порты py-инструментов
RE-2/RE-3 на `native/wire`), `native/model` + `native/records` (SPLIT: бинарный разбор и типы —
без XML; `xmlcompat` — только XML), `native/events.go` + `events_alltrade.go` (реестр декодеров,
`Client.Run`/`Poll` событий), `native/testserver` (публичный тестовый сервер),
`native/internal/oracle` (`LoadFixture`/`CheckDLL`), `native/xmlcompat/render.go` (`Render`),
фикстуры — `native/records/testdata`.

Решения по §8 (не переписывая план):

- §8.2 — `native/testserver` (публичный, не `internal`): его должны импортировать тесты `server/`
  в задаче 29; `internal`-путь невозможен.
- §8.3 — общий помощник oracle-тестов — `native/internal/oracle` (non-test: `_test.go` не
  импортируется); плановый `records.LoadFixture` реализован как `oracle.LoadFixture`; `records`
  не зависит от XML. `checkDLL` = `oracle.CheckDLL(t, catalogFixture, injectFixture, dllXML,
  extra ...records.SecNew)`.
- §8.4 — фикстуры перенесены в `native/records/testdata`; путь в `native/replay/replay_test.go`
  обновлён; сверка `TXML_DEMO_*` теперь `go test -run SecuritiesFull ./native/records`.
- §8.5/§8.6 — «Move» выполнено как SPLIT: `model` — чистые типы (Board/Market/CandleKind/Security/
  Decimal/Trade/AllTrade) без XML-тегов; XML-структуры/эмиттеры остались в `client/commands` и
  `xmlcompat`; `records` не импортирует `client/commands`/`xmlcompat` (проверено `go list -deps`).
- §8.9 — конвенция: каждая семья записей задач 6–28 регистрирует декодер из `init()` в своём
  `native/events_<family>.go`; `events.go` больше не правится. Декодер исполняется под `c.mu`.
- §8.15 — `testserver` — новый API (не перенос `fakeServer`): `New`, `Dial`, `Handle`,
  `Requests`, `Drop`, `Logouts`, `SetMainDelay`; `fakeServer` удалён.
- §8.16 — новые `RQ_*` константы — в файле своей семьи (`native/session/<family>.go`), общий файл
  констант не заводился.
- Дополнительно по ревью: в `native/wire.Keys.Encode` исправлен plain-флаг (0x82 у сервера: payload
  без шифрования; клиент маскирует 0x7f) — иначе replay-сервер не мог отдать 0x82; `replay.Load/Serve`
  режут записанный RQ_MAIN по времени trigger-запроса, как `replay_server.py`; `model.AllTrade`
  перенесён в `model`, чтобы вернуть `model.Trade.Raw`, обещанный планом и `docs/protocol/alltrades.md`.

Проверки Фазы 0 на `a2a4c85`: `make verify` — exit 0; `go test -race ./...` — ok;
`make server_build`, `make queues_build` — exit 0; кросс-сборки linux/amd64, darwin/arm64,
linux/arm64 — ok; `tools/re/check_env.sh --full` — PASS=19 WARN=3 FAIL=0; полная сверка
`SecuritiesFull` на `native/records` — «compared 499 securities». Ревью второй моделью: 4
независимых ревью (`deepseek-flash`-субагенты; Qwen в этой сессии недоступен — маршрут не разрешён,
GLM-адаптер после первого probe перестал регистрироваться), P1/P2 исправлены в тех же задачах.

Не проверено в Фазе 0: живые RE-2/RE-3 (нет загруженного `TC_PASSWORD` и записанной сессии; в Фазе 0
они и не требовались), Windows-прогон тестов (только кросс-сборка). Следующая фаза — Фаза 1 (задачи
5–7); блокеры — отсутствие фикстур `recover-*-live`/`service-dll.xml` (нужны RE-2/RE-3).

---

## 12. Фаза 1: итог и решения Lead (2026-11-04)

**Фаза 1 плана (задачи 5–7) закрыта** на ветке `plan/native-client`; карточки task-7/8/9 — completed.

Коммиты (важнейшие):
- T5: `e8e7868` (offline reconnect/recover), `9a2f2d8` (единый symbol-keyed dedup), `778c450`
  (short path RQ_RELOGIN/RQ_RECOVER), `70b3430` (E05 fixtures), `479151f` (fixture comparison),
  `fe1010d` (DLL-faithful статусы, Reconnect, strict SID), `a6212be` (правки ревью: race,
  фатальные условия, дубли статусов).
- T6: `736932f` (offline service), `c549a9e` (service commands + connect params), `e27dc45`
  (connect options vs DLL), `710a379` (servtime: локальное стенное), `4fcffc6` (review fixes:
  request timeout, e2e, docs), `a0dbb05`+`c97159a` (независимый connect-options эталон).
- T7: `d5a7662` (E07 docs), `7f283d6` (snapshot tail fixtures), `833ba2e` (record formats),
  `105bc6b` (accounts/positions/overnight/union), `6d4a689` (overnight matrix + robustness).
- Инфра: `84b207f` (make verify: gofmt по tracked), `7942973` (маскирование UID), `check_env.sh`
  (та же область gofmt) — в wrap-коммите.

RE: новые документы `docs/protocol/records.md`, `docs/protocol/history.md`; разделы в
`connect.md` (восстановление, параметры `connect`, конец загрузки), `commands.md` (служебные
команды, `get_securities_info`), `dictionaries.md` (RQ_SEC_SPEC, R_SEC_PARAM), `alltrades.md`
(маски, R_QUOTATION/R_QUOTES). Фикстуры: `recover-{short,long}-live`, `service-dll.xml`,
`connect-options-live`, `snapshot-tail-live`, `secparam`/`sec_info_upd`, `secinfo`/`secspec`,
`quotation`/`quotations`, `quote`/`quotes`, `candles-live`.

Решения/отклонения от плана (DLL — эталон, AGENTS §6):
1. **RQ_RELOGIN/RQ_RECOVER**: фактические форматы не совпадают с Interfaces плана —
   `session.ReloginPayload(login)`, `session.RecoverPayload(token, lastSeq)`; sid/uid в payload не
   входят; повторы фиксированные 1 с; `-0xe` повторяется до окна; CRC фатальна только для кадра
   `0x40`; неверный RELOGIN/RECOVER-ответ — немедленно; BYE фатальна.
2. **Автоматического нового логина после `session_timeout` нет** (E05 long: DLL отдаёт
   `connected="false"` и ждёт новый `connect`). Go: `Run` возвращает `*native.RecoveryError`;
   переподключение — явный `Client.Reconnect`. Пункт плана «иначе новый логин» автоматически не
   выполняется — расхождение зафиксировано.
3. **Статусы**: `recover="true"` — в момент обрыва; после успеха plain `connected="true"`; при
   сдаче `connected="false"`. `<overnight status>` DLL отдаёт только перед server_status конца
   загрузки и на команду server_status (E05/E07 S), а не перед recovery/disconnect — план-критерий
   «перед каждым <server_status>» скорректирован по DLL.
4. **§8.10**: в T7 реализованы только `money_position` (R_CLIENT_ASSET 0x3b); бумажные/FORTS-
   позиции — задача 20.
5. **UnknownRecords==0 на всём снапшоте демо** перенесён в задачу 12: в E07-хвосте после 0x3b идёт
   неразобранный `0x5b` (offset 249), полного дампа в git нет; граница закреплена тестом.
   RE-статистика (`records.md`, 7 сессий/3616 ответов) показывает: после задач 7/9/10/12
   неразобранных тегов в живых дампах не остаётся.
6. **`get_securities_info`** — локальная команда (каталог + spec-кэш), в сеть не ходит;
   RQ_SEC_SPEC DLL шлёт после подписки (влияет на задачу 11; сигнатура `Client.SecuritiesInfo`
   сохраняется, данные — из кэша).
7. **`gethistorydata`**: DLL шлёт RQ_HISTORY_EXT (148), RQ_HISTORY (11) не наблюдался; свечи —
   запись 0x09 через RQ_MAIN; точный layout дельта-полей — RE-3 (task-22). `subscribe_ticks`:
   формат подтверждён, но вечером тиков нет — нужен основной сеанс/oracle (task-23).
8. **Песочница/verify**: `make verify` и `tools/re/check_env.sh` проверяют gofmt по
   `git ls-files '*.go'` (gitignored `.re-run/*.go` scratch не должны ломать DoD); AGENTS §7 обновлён.

Проверки Фазы 1 на HEAD: `make verify` exit 0; `go test -race ./...` ok; `make server_build`/
`queues_build` exit 0; `tools/re/check_env.sh --full` — PASS=18 WARN=4 FAIL=0 (WARN: sandbox-кэш,
`~/Library/ghidra`, `TC_PASSWORD` не загружен в текущий шелл — грузится из `~/re/demo.env`).
Живые RE-4: T5 recovery (`recover=true`→plain, без нового RQ_STARTUP, `UnknownRecords` не растёт);
T7 native.Connector на демо — порядок `client`/`positions`/`overnight`/`server_status` как у DLL.

Ревью: по каждой задаче — независимые deepseek-flash-ревьюеры (Qwen-маршрут в этой сессии не
разрешён, в т.ч. после перезапуска DSH; baseURL провайдера пользователь поправил); все P1/P2
исправлены в своих задачах. Не проверено: Windows-прогон, BYE-ветка восстановления живьём, непустой
union, бумажные позиции, тики/свечи живьём (см. п. 4–7 и задачи 12–14).

Следующее — **Фаза 2 (задачи 8–12)**; первая карточка Task 8 (`<sec_info_upd>`) уже в работе.

**Решения пользователя (2026-11-04):** (а) UID демо-аккаунта в старых коммитах истории git
**оставлен как есть** — это временное значение демо-счёта, историю не переписываем (в текущем дереве
значение замаскировано, `7942973`); (б) оракул RE-3 (replay-server + DLL, без реального сервера) —
достаточный путь для торговых команд до Фазы 4; живые торговые операции по-прежнему только по
отдельному согласию; (в) работа продолжается автономно по фазам плана.

---

## 13. Фаза 2: итог и решения Lead (2026-11-04)

**Фаза 2 плана (задачи 8–12) закрыта** на ветке `plan/native-client`; карточки task-21/25/26/27/28 — completed.
(В ветку уже попал каркас задачи 13 — это Фаза 3, см. ниже; PR по фазе включает его как «groundwork».)

Коммиты:
- T8 `<sec_info_upd>`: `c616db1` (декодер/рендер), `6a6e1e7` (эмиссия из Connector), `b05c648`
  (доставка из загрузки через `ClientConfig.OnLoadEvent` + P3 review-фиксы).
- T9 `<quotations>`: `67d019b` (0x06, Subscription API), `7b5953d` (обновление old-теста),
  `561f002` (fixed-форматы денег, firstQuotation, e2e-матрица масок).
- T10 `<quotes>`: `7e20a35` (0x07, batch-тип Quotes, стакан), `f1e2b7a` (old-тест),
  `8cd2128` (P2: `<price>` в XML удаления + edge cases).
- T11 `get_securities_info`: `6809035` (RQ_SEC_SPEC, spec-кэш, `<sec_info>`, команда),
  `181a268` (P2: spec только на subscribe/refetch на reconnect, каталог под `c.mu`).
- T12 остальные записи: `d15e343` (0x6b/0x6e/0x5c/0x34/0x7c, R_WRAPPER best-effort),
  `42427fd` (P1: живой AmBOOK 0x5c по D, R_SYSPARAMS 0x5b, пустая 0x05, strict wrapper,
  E07-хвост 662 записи/0 unknown).
- Инфра/доки: `93a3f70` (sync sec_info_upd/fixtures), `7b47b53` (решения пользователя), RE-коммиты
  `e9b84df` (records.md), `03f223f`/`c978a9e`/`815f163`/`460895b`/`96276bb` (фикстуры/доки),
  `34589ca`→`e61af84` (статистика records.md), `b5ff6f1`/`488fc37`/`b9d54fd` (orders.md),
  `1ace4b3` (0x5b), `3a7f0c8`/`fd35b07`/`5b0e80c` (positions.md).

Решения/отклонения (DLL — эталон, план не переписывается):
1. **`get_securities_info` — локальная команда** (каталог+spec-кэш), в сеть не ходит; RQ_SEC_SPEC
   DLL шлёт после подписки (Go — так же). Первая формулировка плана это не отражала — уточнено в
   `docs/protocol/commands.md`/task-11; сигнатура `Client.SecuritiesInfo` сохранена.
2. **§8.12 (seeding в oracle-тестах):** окна фикстур T8/T9/T11 (secid 15 000+, bond/future) отсутствуют
   в `main1-demo.hex.gz`, поэтому identity/decimals/point_cost и т.п. сеются из ожидаемых XML/S-фактов;
   остальные поля/порядок/форматы проверяются независимо. Полная независимость — отдельная RE-фикстура
   полного снапшота (не блокер; зафиксировано в тестах).
3. **R_QUOTATION/R_QUOTES/R_ORD_NEW/R_TRD/R_ORD_MODIFY/AmBOOK/0x5b/0x05** — форматы восстановлены
   D+O (декомпилятор + оракул RE-3) и закреплены фикстурами; `AmBOOK 0x5c` оказался циклом, а не
   фиксированным форматом; `R_SYSPARAMS 0x5b = L size + blob`; `0x05` — пустая state-запись.
4. **Known-unseen теги** (0x38/0x39/0x42/0x43/0x4f/0x5d/0x6f–0x79) не реализованы вслепую: Records
   даёт на них понятный `UnknownRecordError`, а не тихую потерю; в 8 живых дампах не встречались
   (риск — новая задача при появлении).
5. **Тесты T10/T13 (RED):** для части задач RED отдельно не фиксировался (реализация и тесты в одном
   коммите) — отмечено в отчётах, не выдаётся за проверенное; байтовые oracle-сверки при этом полные.
6. **Секреты:** `UID# 8202` из старых коммитов истории оставлен по решению пользователя (в дереве
   замаскирован); оракул RE-3 (replay-server на 127.0.0.1) — согласованный путь для торговых команд
   до Фазы 4; живые торговые операции — только по отдельному согласию.

Проверки Фазы 2 на HEAD: `make verify` exit 0; `go test -race ./...` ok; `server_build`/
`queues_build` exit 0; `check_env.sh --full` — PASS=19 WARN=2 FAIL=0. Ревью второй моделью:
T8/T9/T10/T11/T12 — P1/P2 исправлены в задачах (`b05c648`, `561f002`, `8cd2128`, `181a268`,
`42427fd`); незакрытых P1/P2 нет.

Следующее — **Фаза 3 (задачи 13–14)**: история/тики; T13 (gethistorydata) уже реализован/на ревью,
T14 (subscribe_ticks) — D-каркас, живые S-фикстуры в основной сессии 5 ноября после 10:00 МСК.

---

## 14. Фазы 3–8: сводка ночной/утренней работы (2026-11-05, перед S-серией)

За ночь 4→5 ноября (и утро 5-го) закрыты все задачи Фаз 3–8, которые не требуют живой сессии.

**Закрытые задачи и ключевые коммиты:**
- T13 gethistorydata: `8cd482b` + ревью-фиксы `8277d6f` (порядок свечей, time bias UTC/wall,
  гонка каталога, non-blocking `<result>`, валидация period/count, диагностика 0x09).
- T15 RQ_TRANSACT: `bf4b770` (`OutcomeError`/`ErrOutcomeUnknown`, `TransactionsInProgress` 156).
- T16 neworder/cancelorder/<orders>: `0feb95e`, `54cca56`, `8be0293` + ревью-фиксы `c6a3143`
  (scale цены, фильтр client id, mask-ветки 0x10, коалесинг батча, загрузочные заявки, RSM-тексты).
- T17 <trades>: `a455d21` + `177879c` (mask≠0 не роняет Run, AccruedInt, Union).
- T18 stop/cond: `d656679`, `e501f11`, `47ea593` + ревью-фиксы `64e7f8b` (leg-флаги 27/26/35 Б,
  cond_type, DLL-тексты, filters, mask-render, non-fatal 0x80).
- T20 <positions>: `e493ea5` (sec_position из R_ASSET, forts через reply 42, money из T7 без дублей).
- T23 margin: `a26e862`, `8705b26`, `b29b757`, `6867d83` + точная формула `a217bc1`
  (`R_SEC_RATE 0x0a` -> V, maxbuy/maxsell по open_balance/lots/0.1428, побайтно 13210/15210).
- T24 news: `7484ffc` (+ `ae44889` C-string/регрессии; `<news_body>` — I до S: `task-56`).
- T25 messages: `73a4acc`/`6cb9697` (R_MSG 0x01 unsized, batch один <messages>, urgent/bias).
- T26 change_pass: `7427b10` (SHA1-payload, reply-коды, empty-reply fatal, без живой смены пароля).
- T27 options: `9b7204f` (RQ 149/153/150/151, гейт версии 0x45, records 0x6f/0x71/0x78, D-XML I).
- T28 RPS/REPO: `b9f305a` (6 команд, 4 O-payload-фикстуры, DLL-тексты, D-XML; dllCommands -> 45).
- T29 (частично): `043d7c9` server.Backend dll|native + TC_BACKEND + native gRPC backend на
  testserver без DLL/Wine; `5aea9d5` Makefile `TC_BACKEND=native make server` (go run, без wine64);
  матрица 45 команд с pending-списком {moveorder, makeordown}.

**RE-материалы (все карточки 49–52, 56–59 закрыты):** margin.md (local render, dead 84/85, marketord,
точная формула maxbuy), news.md (RQ_GETNEWS, 0x35/0x36, news_body I), messages.md (R_MSG), changepass.md,
options.md (версионный гейт), rps.md (payloads/ошибки/литералы), errors/фикстуры.

**Coverage (docs/protocol/coverage.md):** 43/45 XML-команд; не реализованы `moveorder`, `makeordown`
(T19 — живые payload'ы).

**Осталось только live-зависимое (S-серия 5 ноября, основная сессия):** T14 `<ticks>` XML,
T21 oracle `<clientlimits>`, T22 `portfolio_tplus/forts/united` S-формы, T19 `moveorder`/
`makeordown` payload'ы, T24 `<news_body>` (RQ_GETNEWS reply), RE-4 native gRPC на демо,
полный matrix без pending, `docs/protocol/errors.md` (task-64) -> TestServerErrorTexts.

**Проверки:** `make verify`/race зелёные на каждом шаге; живой S-план — по
`docs/deepseek/re-session-checklist.md`; согласие пользователя на минимальный торговый цикл
`neworder→moveorder→cancelorder` (1 лот SBER, цена далеко от рынка, отмена сразу) зафиксировано
**только для демо-счёта** (на проде торговый цикл не выполняется).

---

## 16. Демо-торговый цикл и live-фиксы (2026-11-05, demo.env)

По согласию пользователя (только демо-счёт, 1 лот SBER, цена далеко от рынка, немедленная отмена)
выполнен минимальный цикл; прод не использовался, активных заявок не осталось (все cancelled,
`saldo=300000.0`).

**S-факты (payloads RQ_TRANSACT type=14, закоммичены в `native/records/testdata/transact-*-live.hex`):**
- `neworder` 24 Б: `B01,B00,W secid=26,W board=37,G client,B 'B',B00,L price(×100),L qty,нули`;
  reply `00 <txid> <time> 00`. **Ценовой коридор:** цена 1 отклоняется (`(579)…не меньше 284.62`),
  поэтому использована минимально-далёкая допустимая (285.50), заявка не исполнялась.
- `cancelorder` 5 Б: `B02,B00,L txid,B00`; reply `00`; колбэк `<order status="cancelled">
  <result>{42} Заявка снята.</result></order>`; работает и в новой сессии.
- `moveorder` payload 5 Б: `B02,B01,L txid,B00` (снят для уже отменённых заявок); успешный сценарий
  для активной заявки — **I**: DLL отвечает `{32} Недопустимое значение параметра 'TransactionID'…`
  (D-валидации восстановлены: transactionid/price/moveflag 0..3/quantity для 1–2).
- `makeordown`: transactionid — **атрибут**; `RQ_MAKE_COND_OWN(112)` payload — I,
  O-текст `Cannot make specified transaction_id an own conditional order`.
- Сообщения сервера после транзакций приходят RQ_MAIN **tag `0x0f`** (CP1251-тексты вида
  `(160)…`, `(210)…`) — сплиттер их не знал.

**Live-фиксы (в работе):**
- `ParseOrderNew` для status=6 (cancelled)/removed/denied/rejected: тело короче линейной 52-чтение-схемы
  -> short buffer при загрузке снапшота (7 записей демо); нужны статус-зависимые ветки/безопасный skip.
- `records.Records`: поддержка tag `0x0f` (не падать на серверных текстовых сообщениях).
- RE-4 native backend: direct `Client.Dial` на demo.env OK, raw backend+connect XML даёт
  `connected="true"`, но `TCClient`-стрим закрывается сразу после статуса (EOF) — гонка
  stream/dispatcher/Close, фикс в работе (прод-RE-4 остаётся внешне заблокирован `connected=error`).


---

## 15. Живая S-серия на ПРОДЕ (2026-11-05, read-only)

Пользователь открыл прод-сессию и передал доступ через `~/re/prod.env`; согласие на торговый цикл
распространяется только на демо-счёт, поэтому **торговый цикл `neworder→moveorder→cancelorder` на
проде не выполнялся** (read-only S-серия и RE-4). Все факты проверены DLL 6.47.2.26.4 под Wine через
loopback-tcproxy (S), сырьё — только `.re-run/s/` (gitignored), в git — обезличенные фикстуры/доки.

**Снято (S):**
- **NEWS**: reply `RQ_GETNEWS(73)` **всегда пуст** (len=0) и для заголовков, и для тела; заголовки —
  RQ_MAIN `0x36 flags=1`, тело — `0x36 flags=2` (id + int32 + zlib, CP1251); `lastId` стора —
  **минимальный id**; payloads `get_old_news` (L 0, L INT32_MAX, B count, B 1) и `get_news_body`
  (L id, L id, B 1, B 2). I task-56 закрыт; реализация T14/T24 переработана (см. `204c7ba`,
  `dbace49`, `2d3da44`, `df8c494`).
- **TICKS**: `subscribe_ticks` работает в основную сессию (`<security><board/><seccode/><tradeno/>`,
  board TQBR=75 на проде); тики приходят **RQ_MAIN `0x5a` TICK** (`W secid,W board,G tradeno,
  L time,L price,L quantity,B period,B buysell,L X,L Y`; tradeno/time накопительные) — гипотеза про
  `RQ_TICKS(13)` **опровергнута**; `unsubscribe_ticks` → `Unknown command.`; XML `<ticks><tick>`.
- **LIMITS**: `get_client_limits client=<код>` → success + `<clientlimits>`, C>S `type=43`
  (payload `d2da8d`), без client — `Client not specified.`
- **PORTFOLIOS**: `get_portfolio`/`get_united_portfolio`/`get_mc_portfolio` — **local render**
  (`<portfolio_currency>`/`<united_portfolio>`/`<mc_portfolio>`, без кадров 86/84/85/82);
  `get_portfolio_mct` → `{8} Неверное значение параметра 'szClient'.`; `get_portfolio_tplus/forts`
  → `Unknown command.` (в DLL отсутствуют — план расходился с DLL, DLL эталон).
- Загрузка прода: ~10–12 с, 838 `sec_info_upd`, 61 securities/pits, 5 client, 1 union; server_status
  sys_ver=650 build=41, TZ Russian Standard Time.

**Не снято/блокеры:**
- **OPTIONS**: первый прогон оборвался (`connected=false`, RQ_LOGOUT), затем прод перестал
  устанавливать сессию (`connected=error`, доходят только RQ_LOGIN/RQ_PWD) — похоже на лимит
  логинов/сессий (I); версия/гейт 0x45 и опционные формы — D/I до следующего окна.
- **RE-4 на проде**: native server без Wine поднимается и слушает, но прод-соединение отдаёт
  `connected="error": Ошибка подключения` (вероятно, тот же лимит сессий/логинов после S-серии).
  После фикса `TC_RE4_SEQUENCE` (ожидание `connected=true`, `986b94a`) повторная попытка дала тот же
  внешний блокер; RE-4 перенесён на демо (после открытия 09:50 MSK) — native gRPC backend +
  `connect→get_securities→subscribe→disconnect` без Windows/Wine.

**Гигиена:** прод-хост заменён плейсхолдером `transaq.example` в tracked-файлах (`6d6650e`;
docs/alltrades.md и др.); в истории git значение остаётся (как и UID — решение пользователя не
переписывать историю). Демо-цикл для T19 (moveorder/makeordown) — отдельно, только `demo.env`,
после открытия демо 09:50 MSK.





**RE-4 (native gRPC без Wine):** интеграционный тест `TCClient` на native backend (testserver) проходит
(`2799fe7`); direct `native.Client.Dial` на demo.env — OK; raw backend + connect XML даёт
`connected="true"`. Но 3 попытки RE-4 на живом демо (после cooldown) дали `connected=error`/таймаут 60с —
серверная занятость demo-сессии (single-session/duplicate login, I) — **внешний блокер**, не код;
зафиксировано как partial для T29. Прод-RE-4 остаётся заблокированным (`connected=error`).

**T30 soak:** short live-run на demo.env PASS (`UnknownRecords=0`, `Duplicates=0`, `recoveries=0`,
RSS 30 MiB, `maxGap` 662 мс, 5 мин, 2222–2393 polls, 5946–7523 trades; exit 0) — `820df86`.
Full-day soak (10:00–23:50) в этой сессии не выполнялся — ограничение, зафиксировано.

---

## 17. Итог: Фазы 3–8 + частичная 9 влиты в `main` (2026-11-05)

**PR #50** (`0f12a65..eb4d126`, merge-коммит `eb4d126`) влил в `main` Фазы 3–8 (задачи 13–28) и
частичную Фазу 9 (T29/T30). Все карточки доски закрыты; ветка `plan/native-client` синхронизирована.

**Состояние:**
- `native.Connector` — **45/45 команд** (pending пуст), `server.Backend dll|native` без Wine,
  `TC_BACKEND=native make server`; матрица/ошибки/set_csp/authenticate — `docs/protocol/{coverage,errors,commands}.md`.
- S-серия read-only на проде: news (`<news_header>/<news_body>`), тики (RQ_MAIN `0x5a`), лимиты
  (`<clientlimits>`), портфели (local render); демо-торговый цикл: payloads `neworder`/`cancelorder`/`moveorder`
  (S, активных заявок не осталось); live-фиксы: статус-зависимые тела заявок + `R_RES 0x0f`.
- **Live soak на демо PASS**: 5 мин, `UnknownRecords=0`, `Duplicates=0`, `recoveries=0`, RSS ~30 MiB,
  `maxGap` 662 мс, exit 0 (полный день 10:00–23:50 не выполнялся — ограничение).
- Прод-хост в tracked-файлах заменён плейсхолдером `transaq.example`; в git-истории значение остаётся
  (как и UID — по решению пользователя историю не переписываем).

**Оставшиеся внешние блокеры/ограничения (не код):**
1. **RE-4 live** (native gRPC): direct `Client.Dial` OK, raw backend + connect XML даёт `connected=true`,
   тест `TCClient` на testserver проходит; на живом демо/проде — `connected=error`/duplicate login
   (серверная занятость сессии). Фикс live-load уже сделан; нужна свободная серверная сессия.
2. **Options S** — на проде первый прогон оборвался, затем `connected=error` (лимит логинов); формы D/I,
   нужна новая сессия.
3. **Full-day soak** — не выполнялся (нужен длительный календарный прогон).
4. **I-пункты:** `makeordown`/`RQ_MAKE_COND_OWN 112`, `permissions/rates` (`FUN_18001a700`),
   RPS records/NegDeal/Report (не в Render), typed portfolio API, RQ_FORTS_FIRM_MONEY 47.

**Проверки на `eb4d126`:** `make verify`/race, `server_build`/`queues_build` — зелёные;
`check_env.sh --full` — окружение пригодно; независимые ревью (3 итерации) — P1/P2 исправлены.

---

## 18. Разбор блокеров и общая приёмка (2026-11-05, ветка `fix/acceptance-blockers`)

**«connected=error / duplicate login» — не внешний блокер, а занятая сессия + ошибки клиента:**
- Сервер на RQ_PWD отвечает BYE с байтом причины; `04` = «Пользователь с таким идентификатором уже
  подключен к серверу» (S на проде: занят терминалом пользователя; DLL даёт тот же текст). Таблица
  кодов DLL — `connect.md`. Клиент теперь отдаёт текст DLL вместо «Ошибка подключения» (`866d714`).
- RE-4 на демо падал из-за двух ошибок gRPC-клиента: пример ждал только `ServerStatusChan`, а загрузка
  забивала типизированные каналы; `<securities>` (9,5 МБ одним колбэком — так же делает DLL) не
  пролезал в лимит 4 МБ (`1b8c0fb`). **RE-4 на демо PASS**: native gRPC без Wine,
  connect → connected=true → get_securities → subscribe → disconnect, exit 0, сессия освобождается.
- Найдено оракулом: `gethistorydata` через gRPC/harness обрывал ожидание свечей отменой контекста
  команды и рассинхронизировал поток ответов (`incorrect server response: type 0x94`) — исправлено
  (`6c4f364`).

**Закрытые I:** опционный гейт = `PRO# < 0x45` из RQ_PWD (D) + S-ответы демо (`be7ae08`);
`makeordown` → RQ_MAKE_COND_OWN 112 `L txid` (D+O, `0697dac`); цепочка источников прав/ставок
`FUN_18001a700` (D, `margin.md`).

**Остаётся открытым:** формы XML опционов (на демо нет сервиса, нужен прод с PRO# ≥ 0x45);
источник деревьев ставок и нативные права `get_cln_sec_permissions` (расхождение с DLL записано);
RPS `<negdeal>/<report>` (нет O), typed portfolio API, RQ_FORTS_FIRM_MONEY 47; прод-проверки — пока
логин занят терминалом пользователя; full-day soak.

**Приёмка на ветке:** `make verify` (gofmt/vet/build/кроссы/race) PASS, `server_build`/`queues_build`
PASS, linux/arm64 PASS, `check_env.sh` 15 PASS; live soak демо 3 мин, 6 бумаг: Polls 1298,
Trades 3860, Duplicates 0, UnknownRecords 0, RSS 30 MiB.

**Прод, read-only (2026-11-05, после закрытия терминала пользователем):**
- Нативная загрузка прода падала на `ReadICA` (размер 1 байт, а не W), `R_BOARD` без размера и
  необязательном байте `R_FORTS_SESSION`. Исправлено; колбэки загрузки совпадают с DLL.
- Опционы: формы `<option_families>`/`<family_strikes>` сняты и совпадают с DLL побайтно (кроме
  сессионного `secid`); `family_id` = дни от 1970; `get_options` — пустой ответ = успех.
- Права и ставки: источник — шаблон `RQ_INQUIRE_UT (152)` по `unicat` юниона; `get_cln_sec_permissions`
  совпал с DLL для SBER/GAZP/SiZ6; `&quot;` как в DLL; без лишнего `union`.
- `<messages>` в CP1251 ломали gRPC (invalid UTF-8) — исправлено; `subscribe` с неизвестным `secid` —
  `success` как в DLL.
- **RE-4 на проде PASS** (нативный gRPC без Wine, `TC_RE4_SECID`); soak прода 2,5 мин: Polls 1151,
  Trades 1883, Duplicates 0, UnknownRecords 0.
