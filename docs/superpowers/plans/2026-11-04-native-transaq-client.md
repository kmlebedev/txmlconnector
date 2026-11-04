# Нативный TRANSAQ-клиент на Go: полный API без XML — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go-разработчик получает пакет `github.com/kmlebedev/txmlconnector/native`, который без DLL и без XML выполняет все команды TXmlConnector и отдаёт все сообщения сервера TRANSAQ типизированными Go-структурами. Поверх него `native.Connector` обслуживает старых XML-клиентов побайтно так же, как `txmlconnector64.dll`.

**Architecture:** Протокол восстанавливается из `txmlconnector64-6.47.2.26.4.dll` по единому циклу RE, описанному ниже. Двоичные записи разбирает `native/records`, типы предметной области живут в `native/model`, сеанс и опрос — в `native.Client`. XML строится только в `native/xmlcompat` и только для `native.Connector`. Каждая задача — одно семейство команд и сообщений, проверенное против DLL.

**Tech Stack:** Go 1.27.2 (`go.mod`), stdlib, `golang.org/x/text` (CP1251), `google.golang.org/protobuf` и `github.com/nats-io/nats.go` (только `natsout` и `tools/`). Для RE: Ghidra 12.2 headless, Wine 11.21 + Rosetta (DLL), демо-сервер Finam.

**Spec:** руководство «TRANSAQ XML Connector», ред. 09.09.2025 — https://files.comon.ru/usercontent/TXmlConnector.pdf (далее «PDF», ссылки — номера разделов). Уже восстановленный протокол: `docs/protocol/{connect,dictionaries,commands,alltrades}.md`. Этапы: `docs/ROADMAP-GO.md`.

## Global Constraints

- Эталон поведения — DLL `txmlconnector64-6.47.2.26.4.dll`, SHA-256 `a197e2625e53c0df453687ae1b20ef2ae1a05e034cd20c424a7014b0c9726342`. Где PDF и DLL расходятся, побеждает DLL, расхождение записывается в `docs/protocol/`.
- Пакеты `native/...` собираются с `CGO_ENABLED=0` для linux/darwin × amd64/arm64 и не тянут новых зависимостей, кроме перечисленных в Tech Stack.
- Учётные данные — только из окружения `TC_LOGIN`/`TC_PASSWORD`. Ни логин, ни пароль, ни номера счетов/клиентов/юнионов не попадают в репозиторий, фикстуры, логи и тексты коммитов.
- Фикстуры из живых сессий обезличиваются: код клиента → `TESTCLIENT`, юнион → `TESTUNION`, логин → `TESTLOGIN`. Длины строк при замене сохраняются, если поле фиксированной длины.
- Демо-сервер: `tr1-demo5.finam.ru:3939`, 09:50–23:50 МСК, кроме выходных и праздников. Торговые команды (заявки, отмены, смена пароля) живьём отправляются **только** с явного согласия пользователя в чате, только на демо-счёт, минимальным лотом и по цене, далёкой от рынка.
- Клиент не повторяет отправку `RQ_TRANSACT` сам. Неизвестный исход торговой операции возвращается вызывающему как неопределённый.
- Типы: цены и объёмы, пришедшие целыми в единицах 10^-decimals, хранятся в `model.Decimal` (без `float64`). `float64` допустим только там, где на проводе double (`D`). Время — `time.Time` в UTC.
- Каждое утверждение в `docs/protocol/*.md` помечается статусом: **S** (демо-сервер), **O** (оракул DLL), **D** (декомпилятор), **I** (вывод).
- Комментарии в коде — на английском, документация — на русском (стиль репозитория).
- `go test -race ./...`, `gofmt -l .` (пусто) и `go vet ./...` проходят после каждой задачи.

## Review Focus

1. **Незнакомый тег без размера посреди ответа RQ_MAIN.** Ожидание: события до него доставлены, клиент выдаёт `model.UnknownRecord{Tag, Offset, Payload}`, увеличивает счётчик и продолжает опрос. Без паники и без тихой потери. Тест `TestRunReportsUnknownRecord` — в задаче 3.
2. **Потерян ответ на `RQ_TRANSACT` после отправки.** Ожидание: `NewOrder` возвращает `ErrOutcomeUnknown` с номером транзакции, повторно запрос не отправляется. Исход выясняется через `RQ_TRNS_IN_PROGRESS`. Тест `TestNewOrderLostReplyIsUncertain` — в задаче 15.
3. **Переподключение меняет `secid`.** Ожидание: каталог строится заново, подписки переотправляются по `model.Symbol` с новыми `secid`, у событий после восстановления правильные `Board`/`SecCode`, дубликаты сделок отсеяны. Тест `TestRecoverResubscribesBySymbol` — в задаче 5.
4. **У одной бумаги разная точность цены в разных режимах.** Ожидание: `Decimal` берёт `decimals` из pit-параметров режима события, а не режима по умолчанию. Тест `TestQuotationUsesBoardDecimals` — в задаче 9.
5. **Вызовы API во время `Run`.** Ожидание: `NewOrder`/`History` и т. п. из других горутин не перемешивают кадры с опросом, детектор гонок чист. Тест `TestConcurrentRequestsDuringRun` — в задаче 3.

---

## Как устроен план

Задачи сгруппированы в фазы. Каждая фаза — отдельно поставляемый результат: после неё `go test ./...` зелёный, документация обновлена, PR можно мержить. Фаза 0 обязательна первой. Фазы 1–8 зависят только от неё и между собой — только там, где указано в **Interfaces**. Внутри фазы задачи идут по порядку.

| Фаза | Задачи | Результат |
|---|---|---|
| 0. Фундамент | 1–4 | инструменты RE в репозитории, пакеты `model`/`records`, событийный API `Client.Run` |
| 1. Сеанс | 5–7 | переподключение, служебные команды, сообщения конца загрузки |
| 2. Рыночные данные | 8–12 | `sec_info_upd`, `quotations`, `quotes`, `sec_info`, прочие записи снапшота |
| 3. История и тики | 13–14 | `gethistorydata`, `subscribe_ticks` |
| 4. Торговля | 15–19 | транзакции, заявки, сделки клиента, стоп-/условные заявки, `moveorder` |
| 5. Позиции и портфели | 20–23 | позиции, лимиты, портфели, макс. покупка/продажа, разрешения |
| 6. Новости, сообщения, пароль | 24–26 | новости, текстовые сообщения, `change_pass` |
| 7. Опционы | 27 | семейства, страйки, опционы |
| 8. РПС/РЕПО | 28 | раздел 5 PDF |
| 9. Интеграция | 29–30 | XML-коннектор целиком, gRPC-сервер без Wine, матрица покрытия |

### Карта команд DLL (из `FUN_1801774b0`, проверено на `server_status`, `connect`, `get_connector_version`)

Команда → обработчик (без пометки — нужно соединение):

`set_csp`=`FUN_180197d60` (без соединения), `connect`=`FUN_180197c20` (без соединения), `authenticate`=`FUN_180197da0` (без соединения), `disconnect`=`FUN_180197c60` (без соединения), `gethistorydata`=`FUN_180198020`, `subscribe`=`FUN_1801986a0`, `unsubscribe`=`FUN_180198720`, `makeordown`=`FUN_1801983a0`, `neworder`=`FUN_1801985a0`, `moveorder`=`FUN_180198420`, `change_pass`=`FUN_180197fe0`, `newcondorder`=`FUN_180198460`, `newstoporder`=`FUN_1801985e0`, `newrpsorder`=`FUN_180198560`, `newrepoorder`=`FUN_1801984e0`, `newmrepoorder`=`FUN_1801984a0`, `newreport4`=`FUN_180198520`, `cancelorder`=`FUN_180197f20`, `cancelstoporder`=`FUN_180197fa0`, `cancelnegdeal`=`FUN_180197ee0`, `cancelreport4`=`FUN_180197f60`, `get_forts_positions`=`FUN_180198660`, `get_client_limits`=`FUN_180198620`, `get_markets`=`FUN_180197ca0`, `server_status`=`FUN_180197de0`, `get_securities`=`FUN_180197ce0`, `get_servtime_difference`=`FUN_180197d20`, `get_portfolio`=`FUN_1801980e0`, `get_portfolio_mct`=`FUN_180198120`, `get_united_portfolio`=`FUN_180198160`, `get_mc_portfolio`=`FUN_1801983e0`, `get_max_buy_sell_tplus`=`FUN_180198260`, `get_max_buy_sell`=`FUN_180198220`, `subscribe_ticks`=`FUN_1801986e0`, `get_old_news`=`FUN_1801980a0`, `get_news_body`=`FUN_180198060`, `get_server_id`=`FUN_180197ea0`, `get_connector_version`=`FUN_180197e20` (без соединения), `get_securities_info`=`FUN_180197e60`, `get_united_equity`=`FUN_180198320`, `get_united_go`=`FUN_180198360`, `get_cln_sec_permissions`=`FUN_1801981a0`, `get_options`=`FUN_1801982e0`, `get_option_families`=`FUN_1801982a0`, `get_family_strikes`=`FUN_1801981e0`.

`get_leverage_control` (PDF 3.17) в таблице DLL 6.47 нет: DLL ответит `Unknown command.`, и нативный коннектор повторяет этот ответ.

### Карта запросов (тип → имя → функция-построитель, из вызовов `FUN_180355d50`)

1 RQ_LOGIN, 2 RQ_LOGOUT `FUN_18038d720`, 3 RQ_CHANGE_PWD `FUN_1803b18d0`, 4 RQ_SEND_MESSAGE `FUN_1803b5730`, 5 RQ_MAIN `FUN_18038d7d0`, 9 RQ_STARTUP, 10 RQ_SUBSCRIBE `FUN_180359140`, 11 RQ_HISTORY `FUN_18035a910`, 13 RQ_TICKS `FUN_1803c0ac0`, 14 RQ_TRANSACT `FUN_18039f490`, 15 RQ_SEC_SPEC `FUN_1803bac90`, 16 RQ_DEPO_POS `FUN_1803b3800`, 17 RQ_MONEY_POS `FUN_1803b0b80`, 21 RQ_REPORT_INFO `FUN_1803b2ff0`, 22 RQ_GET_REPORT `FUN_1803b3580`, 42 RQ_CLIENT_HOLDINGS `FUN_1803b3e50`, 43 RQ_CLIENT_LIMITS `FUN_1803b4d50`, 46 RQ_PWD, 47 RQ_FORTS_FIRM_MONEY `FUN_18039e560`, 48 RQ_INDEX_RTS `FUN_18035c2b0`, 50 RQ_TIME `FUN_1803b7ca0`, 51 RQ_GET_SETTLECODE `FUN_1803b2250`, 52 RQ_GET_SEC_SETTLECODE `FUN_1803b27d0`, 63 RQ_RELOGIN / 64 RQ_RECOVER `FUN_1803d31c0`, 72 RQ_RPSREPO_SUBSCR `FUN_1803b7b00`, 73 RQ_GETNEWS `FUN_1803b2d20`, 81 RQ_POSITIONS `FUN_1803b1180`, 82 RQ_GET_RPSREPO `FUN_1803b5c30`, 84 RQ_MARGIN_POS `FUN_180383100`, 85 RQ_MARGIN_POS_LIST `FUN_180382940`, 86 RQ_FOND_PORTFOLIO `FUN_180383d30`, 111 RQ_INQUIRE_SECURITY `FUN_1803864c0`, 112 RQ_MAKE_COND_OWN `FUN_1803861e0`, 120 RQ_SUBSCRIBE_TICKS `FUN_1803ba530`, 148 RQ_HISTORY_EXT `FUN_1803b5c30`, 149 RQ_OPTION_FAMILIES `FUN_1803b92b0`, 150 RQ_FAMILIY_OPTIONS `FUN_1803b9600`, 151 RQ_CERTAIN_OPTIONS `FUN_1803b9cd0`, 152 RQ_INQUIRE_UT `FUN_1803dc4e0`, 153 RQ_FAMILY_STRIKES `FUN_1803b9950`, 156 RQ_TRNS_IN_PROGRESS `FUN_1803de200`, 157 RQ_ENFORCE_TRN_RELEASE `FUN_1803ded20`.

### Карта записей RQ_MAIN (тег → имя → обработчик; `*` — запись с размером)

0x01 R_MSG `FUN_18026c410`; 0x02* R_CLN_NEW `FUN_1803cbc40`; 0x03* R_ORD_NEW `FUN_1803c1b20`; 0x04* R_TRD `FUN_1803c5970`; 0x05 `FUN_180357f90`; 0x06 `FUN_1803bc2f0`; 0x07 `FUN_1803bdc40`; 0x08* R_ALLTRADE (готово); 0x09 `FUN_1803bcf20`; 0x0a–0x0e, 0x38, 0x43 атрибуты бумаги `FUN_1803c1360` (0x0a–0x0e готовы); 0x0f `FUN_1803c7ca0`; 0x10* R_ORD_MODIFY `FUN_1803c3590`; 0x12 `FUN_1803cd060`; 0x13 `FUN_1803cce30`; 0x16 R_SUPERVISOR; 0x17 R_PIPESTATUS; 0x18 R_OVERNIGHT `FUN_180263f50`; 0x19 R_PWD_RESULT; 0x1a `FUN_1803d06b0`; 0x1b `FUN_1803d0c10`; 0x20 R_UPLOAD; 0x28* R_SEC_NEW (готово); 0x2e `FUN_1803c6500`; 0x2f `FUN_1803d1400`; 0x30 `FUN_1803c6720`; 0x31 `FUN_1803d18a0`; 0x32 R_DEPO_CHANGE / 0x33 R_MONEY_CHANGE `FUN_18024cf70`; 0x34 R_BOARD `FUN_180395b40`; 0x35 R_NEWS; 0x36 R_NEWS_UPLOAD; 0x39 R_OPT_EXPIRATION; 0x3a* R_ORD_RENEW `FUN_1803c2d40`; 0x3b* R_CLIENT_ASSET `FUN_1803cde40`; 0x3c `FUN_1803d0530`; 0x3d R_DATE (готово); 0x42 R_CURRENCY_RATE; 0x45 `FUN_1803db4e0`; 0x4f R_DELAY; 0x54* R_STOP `FUN_1803c2850`; 0x56/0x57 `FUN_1803877a0`; 0x58 `FUN_1803c22f0`; 0x59 `FUN_1803c4f50`; 0x5a `FUN_1803bf6c0`; 0x5b `FUN_18035bbb0`; 0x5c `FUN_1803bea70`; 0x5d `FUN_1803962b0`; 0x5e* R_SEC_PARAM `FUN_1803d78a0`; 0x5f* R_WRAPPER (готово); 0x61 `FUN_1803da2e0`; 0x62 `FUN_1803da850`; 0x63 `FUN_1803da980`; 0x65 `FUN_1803da9b0`; 0x66* R_DOUBLELEG `FUN_1803dab80`; 0x67 `FUN_1803dad70`; 0x68 `FUN_1803dae90`; 0x69 `FUN_1803db160`; 0x6b R_FORTS_SESSION; 0x6c `FUN_1803db790`; 0x6d `FUN_1803dbd00`; 0x6e R_EDP_RATE; 0x6f–0x78 `FUN_180396510`…`FUN_180397d40`; 0x79 `FUN_1803de0c0`; 0x7c `FUN_1803983e0`.

Имена без префикса R_ — ещё не установлены, их называет задача, которая разбирает запись.

### Цикл RE (на него ссылаются задачи)

Каждая задача с RE проходит эти процедуры. В шагах задачи указано, какие именно. Команды, установка окружения, таблица функций-читателей DLL и разобранный пример — в [`tools/re/README.md`](../../../tools/re/README.md). Пока не сделана задача 1, вместо `tools/tcproxy` и `tools/replay-server` используются `tools/re/py/proxy.py` и `tools/re/py/replay_server.py`.

- **RE-1 Декомпиляция.** `analyzeHeadless <proj> txc -process txmlconnector64-6.47.2.26.4.dll -noanalysis -readOnly -scriptPath tools/re/ghidra -postScript XrefDecomp.java out.c <depth> a:<адрес>|s:<строка>`; карты — `tools/re/py/dll_maps.py`. Порядок чтения полей записывается в комментарий декодера с адресом функции.
- **RE-2 Живой захват DLL.** `tools/tcproxy` между DLL (`tools/reference-harness` под Wine) и демо-сервером, затем `tools/wire-decode -payload -1`. Из захвата берутся байты запросов и ответов, из вывода harness — XML-колбэки DLL.
- **RE-3 Оракул.** `tools/replay-server` проигрывает DLL записанную сессию и после заданного запроса подмешивает сконструированные записи. XML, который DLL выдаёт для них, фиксирует смысл полей. Нужен для записей, которых демо-сервер не присылает: сделки, заявки, позиции, РПС.
- **RE-4 Живая проверка Go.** Тот же сценарий через нативного клиента и `tools/tcproxy`. Сервер принимает запросы, поток разбирается, `Stats().UnknownRecords` не растёт.

Фикстуры каждой записи кладутся в `native/records/testdata/<запись>-*.hex` (вход) и `<запись>-dll.xml` (ответ DLL). Если источник — живая сессия, имя `-live`; если оракул — `-inject`.

---

## Фаза 0. Фундамент

### Task 1: Прокси и проигрыватель на Go

Рабочие Python-версии уже лежат в `tools/re/` вместе с инструкцией [`tools/re/README.md`](../../../tools/re/README.md): Ghidra-скрипты, `py/proxy.py`, `py/replay_server.py`, `py/stub_server.py`, анализаторы, `run_dll.sh`. Ими можно пользоваться сразу. Задача переносит прокси и проигрыватель на Go поверх `native/wire`, чтобы проигрыватель стал помощником Go-тестов. Python-версии остаются эталоном поведения.

**Files:**
- Exists: `tools/re/ghidra/*.java`, `tools/re/py/*.py`, `tools/re/run_dll.sh`, `tools/re/README.md`
- Create: `tools/tcproxy/main.go`, `tools/tcproxy/main_test.go`
- Create: `native/internal/replay/replay.go`, `native/internal/replay/replay_test.go`
- Create: `tools/replay-server/main.go`

**Interfaces:**
- Produces: формат захвата `"<conn> <sec> C>S|S>C <len> <hex>"` (тот, что уже читает `tools/wire-decode`).
- Produces: `replay.Session` — `func Load(r io.Reader, password string) (*Session, error)`, где вход — вывод `wire-decode -payload -1`; `func (s *Session) Serve(nc net.Conn, inject Injection) error`; `type Injection struct{ AfterRequest byte; Payload []byte }`.

- [ ] **Step 1: Write the failing test** `TestProxyForwardsAndLogs` в `tools/tcproxy/main_test.go`. Эхо-сервер на loopback, прокси между ним и клиентом; клиент пишет `"ping"`, получает `"ping"`. В логе две строки: `1 <t> C>S 4 70696e67` и `1 <t> S>C 4 70696e67`.
- [ ] **Step 2: Run** `go test ./tools/tcproxy` — Expected: FAIL (`undefined: proxy`).
- [ ] **Step 3: Implement** `func proxy(ctx context.Context, ln net.Listener, upstream string, log io.Writer) error` и флаги `-listen 127.0.0.1:39100 -upstream host:port -out capture.log`. Одна горутина на направление, номер соединения растёт с 1, время — секунды от старта с тремя знаками.
- [ ] **Step 4: Run** `go test ./tools/tcproxy` — Expected: PASS.
- [ ] **Step 5: Write the failing test** `TestReplayServesRecordedSession` в `native/internal/replay`. Дамп в формате `wire-decode -payload -1` собирается в тесте из открытых payload:
  - ответ RQ_LOGIN `00080102030405060708`;
  - ответ RQ_PWD — константа `stubPwdReply` из `native/session/login_test.go`, расшифрованная `wire.DefaultKeys().Decode(wire.ServerToClient, …)`, пароль `TESTPASS`;
  - ответ RQ_TIME `74c0a4ae064cff0000c4ff00000000000000000000`;
  - RQ_STARTUP из `startup-demo.hex`, первый RQ_MAIN из `main1-demo.hex.gz`.

  `native.Dial` против `Serve` проходит, `Catalog().Len()==499`. С `Injection{AfterRequest: 10, Payload: alltrades-inject1.hex}` после `SubscribeAllTrades` первый `Poll` возвращает 5 сделок.
- [ ] **Step 6: Implement** `replay.Load`/`Serve`. Сессионный ключ — из записанного ответа RQ_PWD через `session.SessionKeys(password, payload)`. Ответы выдаются по типу запроса в записанном порядке, после исчерпания — пустой payload с флагами `0x82`. Шифрование — `wire.Keys.Encode(wire.ServerToClient, …)`.
- [ ] **Step 7: Run** `go test ./native/internal/replay ./tools/...` — Expected: PASS.
- [ ] **Step 8: `tools/replay-server/main.go`:** флаги `-dump`, `-listen`, `-inject <hex-file>`, `-after <type>`, пароль из `TC_PASSWORD` — те же, что у `py/replay_server.py`. В `tools/re/README.md` команды RE-2/RE-3 дополняются Go-вариантами.
- [ ] **Step 9: Commit** `feat(re): logging proxy, replay server and Ghidra scripts in the repository`.

### Task 2: Пакеты `native/model` и `native/records`

Перенос без изменения поведения: двоичные декодеры уходят из `xmlcompat` в `records`, типы — в `model`. `xmlcompat` остаётся только для XML.

**Files:**
- Create: `native/model/decimal.go`, `native/model/symbol.go`, `native/model/trade.go`, `native/model/security.go`, `native/model/dictionary.go`, `native/model/decimal_test.go`
- Move: из `native/xmlcompat/` в `native/records/` — `records.go`, `startup.go`, `security.go` (только разбор `ParseSecNew`/`SecNew`/`Pit`), `alltrade.go` (`ParseAllTrade`, `AllTrade`, `TradeDedup`), `catalog.go`, `timezone.go`, их тесты и `testdata/*`
- Modify: `native/xmlcompat/*.go` (импорт `records`), `native/client.go`, `native/connector.go`, `native/natsout/natsout.go`, `tools/*`

**Interfaces:**
- Produces: `model.Decimal{Units int64; Scale uint8}` с `func (d Decimal) String() string` (формат `xmlcompat.FormatPrice`: `305.0`, `0.098`, `-305.12`) и `func (d Decimal) Float64() float64`.
- Produces: `model.Symbol{Board, SecCode string}`, `model.Trade` (поля текущего `native.Trade`, но `Price model.Decimal` вместо `Price int64` + `Decimals uint8`), `model.Security`, `model.Board`, `model.Market`, `model.CandleKind`.
- Produces: `records.Records(p []byte) ([]records.Record, error)`, `records.Catalog`, `records.ParseSecNew`, `records.ParseAllTrade`, `records.UnknownRecordError`.

- [ ] **Step 1: Write the failing test** `TestDecimalString` в `native/model/decimal_test.go`: `{30512,2}`→`"305.12"`, `{30500,2}`→`"305.0"`, `{98,3}`→`"0.098"`, `{-30512,2}`→`"-305.12"`, `{7,0}`→`"7"`. `Float64()` для `{30512,2}` = `305.12`.
- [ ] **Step 2: Run** `go test ./native/model` — Expected: FAIL.
- [ ] **Step 3: Implement** `model.Decimal`, перенести типы и файлы, исправить импорты. `native.Trade` заменить на `model.Trade` (без псевдонима).
- [ ] **Step 4: Run** `go test -race ./...` и полную сверку `TXML_DEMO_MAIN=… TXML_DEMO_XML=… go test -run SecuritiesFull ./native/records` — Expected: PASS, вывод побайтно прежний.
- [ ] **Step 5: Commit** `refactor(native): split binary records and typed model out of xmlcompat`.

### Task 3: Событийный API `Client.Run`

**Files:**
- Create: `native/model/event.go`, `native/events.go`
- Modify: `native/client.go`, `native/client_test.go`, `native/connector.go`, `tools/native-trades/main.go`
- Create: `native/internal/testserver/testserver.go` (вынести `fakeServer` из `native/connector_test.go`, чтобы его могли использовать тесты всех пакетов)

**Interfaces:**
- Consumes: `records.Records`, `records.Catalog` (задача 2).
- Produces: `type model.Event interface{ isEvent() }`, `model.UnknownRecord{Tag byte; Offset int; Payload []byte}`.
- Produces: `func (c *Client) Poll(ctx) (events []model.Event, more bool, err error)`, `func (c *Client) Run(ctx context.Context, opt RunOptions, h func([]model.Event) error) error`, `type RunOptions struct{ RqDelay time.Duration }` (по умолчанию 100 мс, минимум 10 мс). Пачка `h` = события одного ответа RQ_MAIN в порядке провода.
- Produces: декодеры регистрируются через `func registerDecoder(tag byte, d func(c *Client, body []byte) ([]model.Event, error))` в `native/events.go` — так задачи 6–28 добавляют записи, не трогая цикл опроса.
- Produces: `testserver.Server` с полями `Snapshot []byte`, `AfterSubscribe []byte`, `MainDelay time.Duration`, методом `Handle(reqType byte, fn func(payload []byte) (flags byte, reply []byte))` для любых запросов и `Requests() []wire.Frame`.

- [ ] **Step 1: Write the failing test** `TestRunReportsUnknownRecord`: ответ RQ_MAIN = запись `R_ALLTRADE` (из `alltrades-inject2.hex`), затем байт `0x7f` и хвост. `Run` выдаёт пачку `[*model.Trade…, model.UnknownRecord{Tag:0x7f}]`, `Stats().UnknownRecords==1`, опрос продолжается: следующий пустой ответ даёт ещё один вызов RQ_MAIN.
- [ ] **Step 2: Write the failing test** `TestConcurrentRequestsDuringRun`: `Run` с `RqDelay=10ms` в горутине. 50 параллельных `SubscribeAllTrades`/`UnsubscribeAllTrades`; testserver проверяет, что каждый кадр расшифровывается (нет перемешанных кадров). Запуск с `-race`.
- [ ] **Step 3: Run** `go test -race ./native` — Expected: FAIL (`Run` undefined).
- [ ] **Step 4: Implement** `Run`/`Poll` поверх реестра декодеров. `R_ALLTRADE` переводится на реестр, `Catalog.Apply` и мутации из #44 остаются в цикле. `Connector` и `native-trades` переходят на `Run`.
- [ ] **Step 5: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 6: Commit** `feat(native): event API Client.Run with a decoder registry`.

### Task 4: Шаблон задачи «семейство» и сверка XML

Общие помощники, чтобы задачи 6–28 сводились к декодеру, типу, XML и тесту.

**Files:**
- Create: `native/records/oracle_test.go` (помощник), `native/xmlcompat/render.go`

**Interfaces:**
- Produces: `func records.LoadFixture(t testing.TB, name string) []byte` (hex или `.hex.gz`).
- Produces: `func xmlcompat.Render(c *records.Catalog, ev model.Event, opt Options) (string, bool)`, где `Options{Milliseconds bool}`, `bool=false` — у события нет XML-формы. Каждая задача добавляет свой case.
- Produces: тестовый шаблон `checkDLL(t, catalogFixture, injectFixture, dllXML string)`: декодирует записи, рендерит события и сравнивает с XML DLL побайтно.

- [ ] **Step 1: Write the failing test** `TestRenderAllTradesThroughRender`: `checkDLL` на `alltrades-inject1.hex`/`alltrades-dll1.xml` даёт совпадение.
- [ ] **Step 2: Run** — Expected: FAIL (`Render` undefined).
- [ ] **Step 3: Implement** `Render` (case `*model.Trade` → текущий `AllTradesXML`) и `checkDLL`.
- [ ] **Step 4: Run** `go test ./native/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(xmlcompat): single Render entry point and DLL oracle test helper`.

---

## Фаза 1. Сеанс

### Task 5: Переподключение и восстановление (RQ_RELOGIN 63, RQ_RECOVER 64)

PDF 4.2 (`recover="true"`), `connect`: `session_timeout`/`request_timeout`. DLL: `FUN_1803d31c0` (построитель обоих), `FUN_1803d27b0` («ReConnecting to»), цикл восстановления в `FUN_180355d50` (ошибки -0xb/-0xd/-0xe, «Recover limit exceeded», «Recover unavailable»).

**Files:**
- Create: `native/session/recover.go`, `native/session/recover_test.go`, `native/recover.go`, `native/recover_test.go`
- Modify: `native/client.go` (поле `subs` по `model.Symbol`), `native/connector.go` (`recover="true"` в статусах), `docs/protocol/connect.md`

**Interfaces:**
- Consumes: `testserver.Server.Drop()` (добавить), `records.Catalog`.
- Produces: `func session.RecoverPayload(sid byte, uid int32, lastSeq ...) []byte` — точная форма по RE-1. `func (c *Client) recover(ctx) error`: в пределах `session_timeout` — RQ_RECOVER (сессия и `secid` сохраняются), иначе — новый логин с пересборкой каталога и переподпиской по `Symbol`.
- Produces: события `model.ServerStatus{Connected bool; Recover bool; Err string}` в потоке `Run`.

- [ ] **Step 1: RE-1** `a:1803d31c0 a:1803d27b0` (глубина 1). Записать в `docs/protocol/connect.md` формат RQ_RELOGIN/RQ_RECOVER и условие выбора между ними.
- [ ] **Step 2: RE-2 (эксперимент E05).** Подключить DLL через `tcproxy`, оборвать соединение прокси на 5 с, затем на 150 с (больше `session_timeout`=120). Сохранить захваты, колбэки `server_status` и `Stats` DLL в `native/session/testdata/recover-{short,long}-live.*` (обезличенные).
- [ ] **Step 3: Write the failing test** `TestRecoverPayloadMatchesDLL`: payload из захвата короткого обрыва.
- [ ] **Step 4: Write the failing test** `TestRecoverResubscribesBySymbol` (Review Focus 3): testserver выдаёт снапшот с SBER `secid`=26, сделка 1; затем `Drop()`, повторный логин со снапшотом, где SBER=27, и сделками 1 (повтор) и 2. Ожидание:
  - `Run` выдаёт `ServerStatus{Connected:true,Recover:true}`;
  - `RQ_SUBSCRIBE` после восстановления содержит `001b002504`;
  - сделка 1 не дублируется, сделка 2 — с `SecCode=="SBER"`.
- [ ] **Step 5: Run** — Expected: FAIL.
- [ ] **Step 6: Implement** восстановление в `Client.Run`: при ошибке транспорта `recover` с интервалами 1, 2, 4… 30 с; статусы в поток; лимит по `session_timeout`.
- [ ] **Step 7: Run** `go test -race ./native/...` — Expected: PASS. **RE-4:** обрыв через `tcproxy` на 5 с — Go восстанавливается без нового снапшота (по захвату: нет RQ_STARTUP).
- [ ] **Step 8: Commit** `feat(native): reconnect and recover like the DLL`.

### Task 6: Служебные команды и параметры `connect`

PDF 3.1 (параметры `connect`), 3.15, 3.16, 3.20, 4.4, 4.18. Обработчики: `connect` `FUN_180197c20`, `get_servtime_difference` `FUN_180197d20`, `get_server_id` `FUN_180197ea0`, `get_markets` `FUN_180197ca0`.

**Files:**
- Create: `native/client_service.go`, `native/client_service_test.go`
- Modify: `native/connector.go`, `native/xmlcompat/render.go`, `docs/protocol/commands.md`

**Interfaces:**
- Produces: `func (c *Client) ServerTimeDifference(ctx) (time.Duration, error)` (RQ_TIME), `func (c *Client) ServerID() int` (SID из RQ_PWD), `func (c *Client) Markets() []model.Market`.
- Produces: поля `ClientConfig` для параметров `connect` из PDF 3.1: `Language string`, `AutoPos, MicexRegisters, Milliseconds, UTCTime bool`, `RqDelay, SessionTimeout, RequestTimeout time.Duration`, `PushULimits, PushPosEquity time.Duration`. Для каждого RE-1 решает, где он действует: в запросах на сервер (RQ_LOGIN/RQ_PWD/RQ_STARTUP), в периодических запросах клиента или только в XML DLL. `Connector.connect` передаёт их из `commands.Connect`.

- [ ] **Step 0: RE-1** `a:180197c20` (глубина 2): разбор `<connect>` и куда уходит каждый параметр. **RE-2:** два живых логина DLL — со всеми параметрами по умолчанию и с `autopos`, `micex_registers`, `utc_time`, `push_u_limits=10`, `push_pos_equity=10`. Разница в захвате — ответ на «где действует». Записать в `docs/protocol/connect.md`.
- [ ] **Step 0b: Write the failing test** `TestConnectOptionsMatchDLL`: запросы Go с теми же параметрами побайтно равны захвату DLL (без пароля и случайного challenge). С `UTCTime` время в XML `<alltrades>` совпадает с ответом DLL (оракул RE-3 на `alltrades-inject1.hex`).
- [ ] **Step 1: RE-1** трёх служебных обработчиков. Определить, ходят ли они в сеть (`get_markets` и `get_server_id` скорее всего отвечают из кэша) и форму ответа (`<servtime_difference>`? — по PDF 3.16). **RE-2:** снять колбэки DLL на демо в `native/testdata/service-dll.xml`.
- [ ] **Step 2: Write the failing tests** `TestServiceCommandsMatchDLL`: три команды через `Connector` дают XML из фикстуры. `TestServerTimeDifference`: при RQ_TIME testserver = `74c0a4ae06…` и локальных часах testserver, сдвинутых на +2 с, результат = `-2s ±1s`.
- [ ] **Step 3: Run** — Expected: FAIL. **Step 4: Implement.** **Step 5: Run** — PASS.
- [ ] **Step 6: Commit** `feat(native): get_servtime_difference, get_server_id, get_markets`.

### Task 7: Сообщения конца загрузки: `<client>`, `<positions>`, `<overnight>`, `<union>`

PDF 4.3, 4.12, 4.34. Наблюдение (`docs/protocol/commands.md`): в конце снапшота DLL отдаёт `<client>`, `<positions>`, `<overnight status="false"/>` перед `server_status`. Записи: 0x02* R_CLN_NEW `FUN_1803cbc40`, 0x18 R_OVERNIGHT `FUN_180263f50`, 0x62 `FUN_1803da850`, 0x67 `FUN_1803dad70`, 0x69 `FUN_1803db160` (последние три идут подряд в конце снапшота демо).

**Files:**
- Create: `native/records/client.go`, `native/records/client_test.go`, `native/model/client.go`, `native/client_accounts.go`
- Modify: `native/xmlcompat/render.go`, `native/connector.go`

**Interfaces:**
- Produces: `model.ClientAccount{ID, Type, Currency, Union, FortsAcc string; Market int; Removed bool}`, `model.Overnight{Status bool}`, `model.Union{ID string; Removed bool}`, `func (c *Client) Accounts() []model.ClientAccount`.

- [ ] **Step 1: RE-1** обработчиков 0x02, 0x18, 0x62, 0x67, 0x69. **RE-2:** последний ответ снапшота демо и колбэки DLL `<client>`/`<positions>`/`<overnight>` — фикстура `snapshot-tail-live.hex` + `snapshot-tail-dll.xml`, обезличенная.
- [ ] **Step 2: Write the failing test** `TestSnapshotTailMatchesDLL` через `checkDLL`. `Stats().UnknownRecords==0` на всём снапшоте демо (`TXML_DEMO_MAIN`).
- [ ] **Step 3: Run** — FAIL. **Step 4: Implement** декодеры и рендер. Порядок колбэков `Connector` перед `server_status`: `<client>`, `<positions>`, `<overnight>`. Перед каждым `<server_status>` — `<overnight status="false"/>`, как у DLL.
- [ ] **Step 5: Run** — PASS; **RE-4**: native `Connector` на демо отдаёт ту же последовательность колбэков, что DLL.
- [ ] **Step 6: Commit** `feat(native): client accounts, overnight and union at the end of the load`.

---

## Фаза 2. Рыночные данные

### Task 8: `<sec_info_upd>` из R_SEC_PARAM (0x5e)

PDF 4.22. Маски и сеттеры R_SEC_PARAM разобраны в #44 (`docs/protocol/dictionaries.md`), форма — по 684 живым событиям. Обработчик `FUN_1803d78a0`.

**Files:**
- Create: `native/records/secparam.go`, `native/records/secparam_test.go`, `native/model/secinfo.go`
- Modify: `native/xmlcompat/render.go`, `native/events.go`

**Interfaces:**
- Produces: `model.SecInfoUpdate{Symbol; SecID uint16; Market int; MinPrice, MaxPrice, BuyDeposit, SellDeposit, BgoC, BgoNC, BgoBuy, PointCost *model.Decimal}` — указатели: в событии только изменившиеся поля (PDF 4.22).

- [ ] **Step 1: Write the failing test** `TestSecInfoUpdMatchesDLL`: живая фикстура R_SEC_PARAM + 684 колбэка DLL (`secparam-live.hex.gz`, `sec_info_upd-dll.xml.gz` из захвата #44) побайтно.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** декодер и рендер. **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(native): sec_info_upd from R_SEC_PARAM`.

### Task 9: `<quotations>` — подписка (маска 0x01) и запись 0x06

PDF 3.5, 4.7. Подписка подтверждена (маска 5 на демо). Запись 0x06 пришла один раз после подписки на котировки (`docs/protocol/alltrades.md`), обработчик `FUN_1803bc2f0`. В `<quotations>` приходят только изменившиеся поля.

**Files:**
- Create: `native/records/quotation.go`, `native/records/quotation_test.go`, `native/model/quotation.go`, `native/client_subscribe.go`
- Modify: `native/client.go` (`SubscribeAllTrades` → обёртка над `Subscribe`), `native/connector.go`, `native/xmlcompat/render.go`

**Interfaces:**
- Produces: `type Subscription struct{ AllTrades, Quotations, Quotes []model.Symbol }`, `func (c *Client) Subscribe(ctx, Subscription) error`, `func (c *Client) Unsubscribe(ctx, Subscription) error`.
- Produces: `model.Quotation{Symbol; SecID uint16; Fields QuotationFields}`. `QuotationFields` — все элементы PDF 4.7 (`accruedintvalue`, `open`, `waprice`, `biddepth`, `biddeptht`, `numbids`, `offerdepth`, `offerdeptht`, `bid`, `offer`, `numoffers`, `numtrades`, `voltoday`, `openpositions`, `deltapositions`, `last`, `quantity`, `time`, `change`, `priceminusprevwaprice`, `valtoday`, `yield`, `yieldatwaprice`, `marketpricetoday`, `highbid`, `lowoffer`, `high`, `low`, `closeprice`, `closeyield`, `status`, `tradingstatus`, `buydeposit`, `selldeposit`, `volatility`, `theoreticalprice`, `bgo_buy`, `point_cost`, `lcurrentprice`) как указатели: `nil` — поле не приходило.

- [ ] **Step 1: RE-1** `a:1803bc2f0` (глубина 1): порядок полей и маски. **RE-2:** подписка на `quotations` для TQBR:SBER и FUT:SiZ6 в основную сессию; фикстуры `quotation-live.hex` + `quotations-dll.xml`. **RE-3:** записи с изменённой точностью режима — пары режимов с разными `decimals` из каталога.
- [ ] **Step 2: Write the failing tests:**
  - `TestQuotationsMatchDLL` (`checkDLL`);
  - `TestSubscribeMasksMatchDLL`: payload с масками 1 и 5 из живых захватов;
  - `TestQuotationUsesBoardDecimals` (Review Focus 4): одна бумага в режимах с `decimals` 2 и 4, `last` у события второго режима = `Decimal{Scale:4}`.
- [ ] **Step 3: Run** — FAIL. **Step 4: Implement.** **Step 5: Run** — PASS. **RE-4** на демо.
- [ ] **Step 6: Commit** `feat(native): quotations subscription and updates`.

### Task 10: `<quotes>` — стакан (маска 0x02) и запись 0x07

PDF 3.5, 4.9. Бит маски `0x02` пока вывод (**I**), запись 0x07 `FUN_1803bdc40` — кандидат.

**Files:**
- Create: `native/records/quote.go`, `native/records/quote_test.go`, `native/model/quote.go`
- Modify: `native/client_subscribe.go`, `native/xmlcompat/render.go`

**Interfaces:**
- Produces: `model.Quote{Symbol; SecID uint16; Price model.Decimal; Source string; Yield *int; Buy, Sell int64}`. По PDF 4.9 `buy`/`sell = -1` означает удаление уровня; в типе это `Buy == -1`, без отдельного флага.

- [ ] **Step 1: RE-1** `a:1803bdc40`, `a:180359140` (бит 0x02). **RE-2:** подписка на `quotes` SBER в основную сессию, фикстуры `quote-live.hex`, `quotes-dll.xml`.
- [ ] **Step 2: Write the failing test** `TestQuotesMatchDLL` и `TestSubscribeQuotesMask` (байт маски из захвата).
- [ ] **Step 3–5:** Run FAIL → Implement → Run PASS; **RE-4**.
- [ ] **Step 6: Commit** `feat(native): order book quotes`.

### Task 11: `get_securities_info` → `<sec_info>` (RQ_SEC_SPEC 15, RQ_INQUIRE_SECURITY 111)

PDF 3.24, 4.21. Обработчик команды `FUN_180197e60`. DLL сама шлёт RQ_SEC_SPEC (список `W secid`) после подписки (`docs/protocol/alltrades.md`).

**Files:**
- Create: `native/session/secspec.go` + тест, `native/records/secinfo.go` + тест, `native/client_secinfo.go`
- Modify: `native/model/secinfo.go`, `native/xmlcompat/render.go`, `native/connector.go`

**Interfaces:**
- Produces: `func (c *Client) SecuritiesInfo(ctx, symbols ...model.Symbol) ([]model.SecInfo, error)`; `model.SecInfo` — все поля PDF 4.21 (`secname`, `pname`, `mat_date`, `clearing_price`, `minprice`, `maxprice`, `buy_deposit`, `sell_deposit`, `bgo_c`, `bgo_nc`, `bgo_buy`, `accruedint`, `coupon_value`, `coupon_date`, `coupon_period`, `facevalue`, `put_call`, `point_cost`, `opt_type`, `lot_volume`, `isin`, `regnumber`, `buybackprice`, `buybackdate`, `currencyid`).

- [ ] **Step 1: RE-1** `a:180197e60 a:1803bac90 a:1803864c0`: какой запрос несёт `get_securities_info` и форма ответа (ответ RQ_SEC_SPEC в захвате — 273 байта на 4 бумаги). **RE-2:** `get_securities_info` для облигации (TQCB/TQOB), акции и фьючерса; фикстуры.
- [ ] **Step 2: Write the failing tests** `TestSecSpecPayloadMatchesDLL` (`00040379017b001a` из захвата), `TestSecInfoMatchesDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS; **RE-4**.
- [ ] **Step 6: Commit** `feat(native): get_securities_info`.

### Task 12: Остальные записи снапшота и обновлений справочников

Цель: `Stats().UnknownRecords==0` на полной живой сессии с подписками. Записи: 0x66* R_DOUBLELEG `FUN_1803dab80`, 0x34 R_BOARD `FUN_180395b40` (новый режим во время сессии), 0x38 R_SEC_RESIDENT, 0x43 R_SEC_DEFSETTLECODE, 0x42 R_CURRENCY_RATE, 0x6b R_FORTS_SESSION, 0x6e R_EDP_RATE, 0x4f R_DELAY, 0x39 R_OPT_EXPIRATION, 0x5d, 0x6f–0x7c.

**Files:**
- Create: `native/records/misc.go` + тест (по одной функции на запись), `native/model/misc.go`

**Interfaces:**
- Produces: для каждой записи с XML-формой — свой тип события. Для записей без формы в PDF — `model.SecurityAttr{SecID uint16; Attr string; Value int64}` или `model.SessionInfo{...}`; имя поля — по строкам из обработчика (RE-1).

- [ ] **Step 1: RE-1** перечисленных обработчиков. Для каждой — формат, имя, есть ли XML-колбэк (оракул RE-3: подмешать запись, посмотреть вывод DLL).
- [ ] **Step 2: Write the failing test** `TestFullLiveSessionHasNoUnknownRecords`: полный живой дамп с подписками на `alltrades`+`quotations`+`quotes` (переменные `TXML_DEMO_*`, пропуск без них) — `UnknownRecords==0`. Плюс `checkDLL` для каждой записи с XML.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(records): remaining snapshot and dictionary update records`.

---

## Фаза 3. История и тики

### Task 13: `gethistorydata` → `<candles>` (RQ_HISTORY 11, RQ_HISTORY_EXT 148)

PDF 3.7, 4.1 (`status` 0–3). Обработчик команды `FUN_180198020`, построители `FUN_18035a910` и `FUN_1803b5c30`. Строки «RQ_HISTORY incorrect server response».

**Files:**
- Create: `native/session/history.go` + тест, `native/records/candles.go` + тест, `native/model/candle.go`, `native/client_history.go` + тест
- Modify: `native/xmlcompat/render.go`, `native/connector.go`

**Interfaces:**
- Produces: `type HistoryRequest struct{ Symbol model.Symbol; Period int /* candlekind id */; Count int; Reset bool }`, `func (c *Client) History(ctx, HistoryRequest) (model.Candles, error)`. `model.Candles{Symbol; Period int; Status int; Items []model.Candle}`, `model.Candle{Time time.Time; Open, High, Low, Close model.Decimal; Volume int64; OpenInterest *int64}`.
- Правило: если DLL получает свечи в ответе RQ_HISTORY — метод возвращает их сразу. Если частями через RQ_MAIN — метод собирает части до `status`≠2 и возвращает целиком, а `Run` их не выдаёт.

- [ ] **Step 1: RE-1** трёх функций: какой запрос когда (EXT — расширенный период?), формат ответа. **RE-2:** `gethistorydata` SBER период 1 (1 мин) count 10 reset true; затем count 1000 (несколько частей, `status` 2→1/0); фикстуры.
- [ ] **Step 2: Write the failing tests** `TestHistoryPayloadMatchesDLL`, `TestCandlesMatchDLL`, `TestHistoryCollectsParts` (status 2, 2, 0 → одна выдача).
- [ ] **Step 3–5:** FAIL → Implement → PASS; **RE-4**.
- [ ] **Step 6: Commit** `feat(native): gethistorydata`.

### Task 14: `subscribe_ticks` → `<ticks>` (RQ_SUBSCRIBE_TICKS 120, RQ_TICKS 13)

PDF 3.19, 4.17. Обработчик `FUN_1801986e0`, построители `FUN_1803ba530`, `FUN_1803c0ac0`.

**Files:**
- Create: `native/session/ticks.go` + тест, `native/records/ticks.go` + тест, `native/model/tick.go`, `native/client_ticks.go`

**Interfaces:**
- Produces: `type TickRequest struct{ Symbol model.Symbol; TradeNo int64 }`, `func (c *Client) SubscribeTicks(ctx, filter bool, reqs ...TickRequest) error`. Событие `model.Tick{Symbol; SecID uint16; TradeNo int64; Time time.Time; Price model.Decimal; Quantity int64; Period byte; BuySell byte; OpenInterest *int64}`.

- [ ] **Step 1: RE-1**, **RE-2** (тики SBER от `tradeno` 0), фикстуры.
- [ ] **Step 2: Write the failing tests** `TestTicksPayloadMatchesDLL`, `TestTicksMatchDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS; **RE-4**.
- [ ] **Step 6: Commit** `feat(native): subscribe_ticks`.

---

## Фаза 4. Торговля

Все живые шаги этой фазы — только с согласия пользователя в чате, на демо-счёте, 1 лот, цена далеко от рынка, отмена сразу после постановки.

### Task 15: Транзакции RQ_TRANSACT (14): номер, ответ, неопределённый исход

PDF 3.8 (`<result success="true" transactionid="…"/>`). Построитель `FUN_18039f490`, строки `TRN_NEWORDER`, `TRN_CANCELORDER`, `RQ_TRNS_IN_PROGRESS` (156), `RQ_ENFORCE_TRN_RELEASE` (157).

**Files:**
- Create: `native/session/transact.go`, `native/session/transact_test.go`, `native/model/transaction.go`

**Interfaces:**
- Produces: `type session.TxKind byte` (значения `TRN_*` по RE-1), `func (c *Conn) Transact(ctx, kind TxKind, body []byte) (model.TransactionID, error)`.
- Produces: `var ErrOutcomeUnknown = errors.New(...)` и `type OutcomeError struct{ TransactionID model.TransactionID; Err error }` (`Is(ErrOutcomeUnknown)`). `func (c *Conn) TransactionsInProgress(ctx) ([]model.TransactionID, error)` (RQ 156).
- Правило: транспортная ошибка после записи кадра — `OutcomeError`. Ошибка до записи — обычная ошибка. Повторной отправки нет.

- [ ] **Step 1: RE-1** `a:18039f490 a:1803de200 a:1803ded20`: заголовок транзакции, номер (клиентский или серверный), коды `TRN_*`, ответ. **RE-3:** оракул ответа RQ_TRANSACT с ошибкой сервера — текст для `<result success="false">`.
- [ ] **Step 2: Write the failing test** `TestNewOrderLostReplyIsUncertain` (Review Focus 2): testserver принимает RQ_TRANSACT и закрывает соединение без ответа. `Transact` возвращает `errors.Is(err, ErrOutcomeUnknown)` и `TransactionID != 0`; в `testserver.Requests()` ровно один RQ_TRANSACT.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(session): RQ_TRANSACT with explicit unknown outcome`.

### Task 16: `neworder`, `cancelorder` и поток заявок `<orders>`

PDF 3.8, 3.11, 4.10. Записи 0x03* R_ORD_NEW `FUN_1803c1b20`, 0x10* R_ORD_MODIFY `FUN_1803c3590`, 0x3a* R_ORD_RENEW `FUN_1803c2d40`.

**Files:**
- Create: `native/records/order.go` + тест, `native/model/order.go`, `native/client_orders.go` + тест
- Modify: `native/xmlcompat/render.go`, `native/connector.go`

**Interfaces:**
- Produces: `model.NewOrder{Client, Union string; Symbol model.Symbol; Price model.Decimal; Hidden, Quantity int64; BuySell byte; ByMarket, UseCredit, NoSplit bool; Unfilled string /* PutInQueue|FOK|IOC */; ExpDate *time.Time; BrokerRef string}` — поля PDF 3.8.
- Produces: `func (c *Client) NewOrder(ctx, model.NewOrder) (model.TransactionID, error)`, `func (c *Client) CancelOrder(ctx, model.TransactionID) error`. Событие `model.Order` — поля PDF 4.10 (`transactionid`, `orderno`, `secid`, `board`, `seccode`, `client`, `union`, `status`, `buysell`, `time`, `accepttime`, `brokerref`, `value`, `accruedint`, `settlecode`, `balance`, `price`, `quantity`, `hidden`, `yield`, `withdrawtime`, `condition`, `conditionvalue`, `validafter`, `validbefore`, `maxcomission`, `result`).

- [ ] **Step 1: RE-1** обработчиков команд `FUN_1801985a0`, `FUN_180197f20` и записей. **RE-2 (с согласия):** заявка 1 лот SBER далеко от рынка, отмена; фикстуры запросов, записей и колбэков `<orders>`, обезличенные. **RE-3:** записи статусов, которых не добиться живьём (`matched`, `rejected`).
- [ ] **Step 2: Write the failing tests** `TestNewOrderPayloadMatchesDLL`, `TestCancelOrderPayloadMatchesDLL`, `TestOrdersMatchDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): neworder, cancelorder and order updates`.

### Task 17: Сделки клиента `<trades>` (R_TRD 0x04)

PDF 4.11. Обработчик `FUN_1803c5970`, XML — `FUN_180127db0` (уже прочитан: порядок `secid, tradeno, orderno, board, seccode, client, buysell, union, time, brokerref, value, comission, …`).

**Files:**
- Create: `native/records/clienttrade.go` + тест, `native/model/clienttrade.go`
- Modify: `native/xmlcompat/render.go`

**Interfaces:**
- Produces: событие `model.ClientTrade` — поля PDF 4.11.

- [ ] **Step 1: RE-1**; **RE-3** (оракул; живьём — только если исполнилась заявка из задачи 16, что маловероятно при цене далеко от рынка).
- [ ] **Step 2: Write the failing test** `TestClientTradesMatchDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): client trades`.

### Task 18: `newcondorder`, `newstoporder`, `cancelstoporder`, R_STOP (0x54)

PDF 3.9, 3.10, 3.12, 4.10 (`<stoporder>`). Обработчики `FUN_180198460`, `FUN_1801985e0`, `FUN_180197fa0`; запись `FUN_1803c2850`.

**Files:**
- Create: `native/records/stoporder.go` + тест, `native/model/stoporder.go`, `native/client_stoporders.go`
- Modify: `native/model/order.go` (условия `newcondorder`), `native/xmlcompat/render.go`

**Interfaces:**
- Produces: `model.NewCondOrder` (поля PDF 3.9: `cond_type`, `cond_value`, `validafter`, `validbefore`, …), `model.NewStopOrder` (PDF 3.10: `stoploss`, `takeprofit`, `linkedorderno`, …), `func (c *Client) NewCondOrder(...)`, `NewStopOrder(...)`, `CancelStopOrder(ctx, model.TransactionID) error`, событие `model.StopOrder`.

- [ ] **Step 1: RE-1**; **RE-2** (с согласия, стоп далеко от рынка, отмена); **RE-3** для статусов.
- [ ] **Step 2: Write the failing tests** по образцу задачи 16 для трёх команд и `TestStopOrdersMatchDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): conditional and stop orders`.

### Task 19: `moveorder` и `makeordown`

PDF 3.25. Обработчики `FUN_180198420` (`moveorder`), `FUN_1801983a0` (`makeordown` — в PDF не описана, назначение установить по RE-1 и `RQ_MAKE_COND_OWN` 112 `FUN_1803861e0`).

**Files:**
- Modify: `native/client_orders.go` + тест, `native/session/transact.go`

**Interfaces:**
- Produces: `func (c *Client) MoveOrder(ctx, id model.TransactionID, price model.Decimal, quantity int64, moveFlag int) (model.TransactionID, error)` (PDF 3.25 `moveflag` 0/1/2). Для `makeordown` — метод по итогам RE-1 или явное «не поддерживается» в `docs/protocol/orders.md` с причиной.

- [ ] **Step 1: RE-1**; **RE-2** (с согласия).
- [ ] **Step 2: Write the failing test** `TestMoveOrderPayloadMatchesDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): moveorder`.

---

## Фаза 5. Позиции и портфели

### Task 20: Поток `<positions>`: деньги, бумаги, FORTS

PDF 4.12 (`money_position`, `sec_position`, `forts_position`, `forts_money`, `forts_collaterals`, `spot_limit`, `united_limits`). Записи 0x32 R_DEPO_CHANGE, 0x33 R_MONEY_CHANGE (`FUN_18024cf70`), 0x3b* R_CLIENT_ASSET `FUN_1803cde40`. Запросы 16 RQ_DEPO_POS, 17 RQ_MONEY_POS, 81 RQ_POSITIONS.

**Files:**
- Create: `native/records/positions.go` + тест, `native/model/position.go`, `native/client_positions.go`
- Modify: `native/xmlcompat/render.go`

**Interfaces:**
- Produces: события `model.MoneyPosition`, `model.SecPosition`, `model.FortsPosition`, `model.FortsMoney`, `model.FortsCollaterals`, `model.SpotLimit`, `model.UnitedLimits` — поля PDF 4.12. Существующие XML-структуры `client/commands` (`MoneyPosition`, `SecPosition`, `FortsPosition`, …) используются только в `xmlcompat`.

- [ ] **Step 1: RE-1**; **RE-2:** позиции демо-счёта в конце загрузки; фикстуры обезличиваются (`client` → `TESTCLIENT`). **RE-3:** изменения позиций.
- [ ] **Step 2: Write the failing test** `TestPositionsMatchDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS; **RE-4**.
- [ ] **Step 6: Commit** `feat(native): positions`.

### Task 21: `get_forts_positions`, `get_client_limits` (RQ_CLIENT_LIMITS 43, RQ_FORTS_FIRM_MONEY 47)

PDF 3.13, 3.14, 4.13 (`<clientlimits>`). Обработчики `FUN_180198660`, `FUN_180198620`.

**Files:**
- Create: `native/session/limits.go` + тест, `native/records/limits.go` + тест, `native/model/limits.go`
- Modify: `native/client_positions.go`, `native/xmlcompat/render.go`, `native/connector.go`

**Interfaces:**
- Produces: `func (c *Client) FortsPositions(ctx, client string) error` (данные приходят событиями задачи 20 — проверить по RE-1), `func (c *Client) ClientLimits(ctx, client string) (model.ClientLimits, error)`.

- [ ] **Step 1: RE-1**; **RE-2** для FORTS-счёта демо, если доступен (иначе **RE-3**).
- [ ] **Step 2: Write the failing tests** `TestClientLimitsPayloadMatchesDLL`, `TestClientLimitsMatchDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): forts positions and client limits`.

### Task 22: Портфели: `get_portfolio`, `get_portfolio_mct`, `get_mc_portfolio`, `get_united_portfolio`, `get_united_equity`, `get_united_go`

PDF 3.26, 3.28–3.32, 4.25, 4.28–4.31, 4.36. Обработчики `FUN_1801980e0`, `FUN_180198120`, `FUN_1801983e0`, `FUN_180198160`, `FUN_180198320`, `FUN_180198360`. Запросы-кандидаты: 86 RQ_FOND_PORTFOLIO `FUN_180383d30`, 42 RQ_CLIENT_HOLDINGS `FUN_1803b3e50` (DLL шлёт его один раз за сессию), 152 RQ_INQUIRE_UT `FUN_1803dc4e0`.

**Files:**
- Create: `native/session/portfolio.go` + тест, `native/records/portfolio.go` + тест, `native/model/portfolio.go`, `native/client_portfolio.go`
- Modify: `native/xmlcompat/render.go`, `native/connector.go`

**Interfaces:**
- Produces: `func (c *Client) PortfolioTPlus(ctx, client string) (model.PortfolioTPlus, error)`, `PortfolioMCT(ctx, client string)`, `MCPortfolio(ctx, model.MCPortfolioRequest)` (поля — атрибуты команды PDF 3.30: `client`, `union`, `currency`, `asset`, `money`, `depo`, `registers`, `maxbs`), `UnitedPortfolio(ctx, client, union string)`, `UnitedEquity(ctx, union string)`, `UnitedGO(ctx, union string)`. Типы — поля PDF 4.25, 4.28–4.31, 4.36. Существующие `commands.UnitedPortfolio`/`UnitedEquity` — только для `xmlcompat`.

- [ ] **Step 1: RE-1** шести обработчиков и трёх запросов: какая команда какой запрос шлёт и что приходит в ответе, а что через RQ_MAIN. **RE-2** на демо-счёте.
- [ ] **Step 2: Write the failing tests** — по паре (`…PayloadMatchesDLL`, `…MatchesDLL`) на команду.
- [ ] **Step 3–5:** FAIL → Implement → PASS; **RE-4**.
- [ ] **Step 6: Commit** `feat(native): portfolios`.

### Task 23: `get_max_buy_sell_tplus`, `get_max_buy_sell`, `get_cln_sec_permissions`, режим кредитования

PDF 3.27, 3.33, 3.34, 4.14–4.16, 4.32, 4.35, 4.37. Обработчики `FUN_180198260`, `FUN_180198220`, `FUN_1801981a0`; запросы 84 RQ_MARGIN_POS `FUN_180383100`, 85 RQ_MARGIN_POS_LIST `FUN_180382940`.

**Files:**
- Create: `native/session/margin.go` + тест, `native/records/margin.go` + тест, `native/model/margin.go`, `native/client_margin.go`

**Interfaces:**
- Produces: `func (c *Client) MaxBuySellTPlus(ctx, client string, symbols ...model.Symbol) ([]model.MaxBuySell, error)`, `MaxBuySell(ctx, client, union string, symbols ...model.Symbol)`, `ClientSecPermissions(ctx, client string, sym model.Symbol) (model.SecPermissions, error)`, события `model.Leverage`/`model.MarketOrdAllowed` для 4.14–4.16, если они приходят через RQ_MAIN.

- [ ] **Step 1: RE-1**; **RE-2**.
- [ ] **Step 2: Write the failing tests** по паре на команду.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): max buy/sell and security permissions`.

---

## Фаза 6. Новости, сообщения, пароль

### Task 24: Новости: `get_old_news`, `get_news_body`, поток `<news_header>`

PDF 3.21, 3.22, 4.19, 4.20. Обработчики `FUN_1801980a0`, `FUN_180198060`; запрос 73 RQ_GETNEWS `FUN_1803b2d20`; записи 0x35 R_NEWS, 0x36 R_NEWS_UPLOAD.

**Files:**
- Create: `native/session/news.go` + тест, `native/records/news.go` + тест, `native/model/news.go`, `native/client_news.go`

**Interfaces:**
- Produces: `func (c *Client) OldNews(ctx, count int) error` (заголовки приходят событиями `model.NewsHeader{ID int64; Time time.Time; Source, Title string}`), `func (c *Client) NewsBody(ctx, id int64) (model.NewsBody, error)`.

- [ ] **Step 1: RE-1**; **RE-2** (новости на демо, если идут).
- [ ] **Step 2–5:** тесты `TestOldNewsPayloadMatchesDLL`, `TestNewsMatchDLL` → FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): news`.

### Task 25: Текстовые сообщения `<messages>` (R_MSG 0x01)

PDF 4.33. Обработчик `FUN_18026c410`.

**Files:**
- Create: `native/records/message.go` + тест, `native/model/message.go`

**Interfaces:**
- Produces: событие `model.Message{Time time.Time; Urgent bool; From, Text string}`.

- [ ] **Step 1: RE-1**; **RE-3**.
- [ ] **Step 2–5:** `TestMessagesMatchDLL` → FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): text messages`.

### Task 26: `change_pass` (RQ_CHANGE_PWD 3)

PDF 3.18. Обработчик `FUN_180197fe0`, построитель `FUN_1803b18d0`. Только **RE-3**: живая смена пароля демо-счёта запрещена без согласия пользователя.

**Files:**
- Create: `native/session/changepwd.go` + тест, `native/client_password.go`

**Interfaces:**
- Produces: `func (c *Client) ChangePassword(ctx, oldPassword, newPassword string) error`; после успеха ключ пароля для `recover` (задача 5) обновляется.

- [ ] **Step 1: RE-1** (как шифруется пароль — ГОСТ-ключ из SHA-1, как в RQ_PWD?). **RE-3:** DLL с `change_pass` против `replay-server` — снять запрос.
- [ ] **Step 2: Write the failing test** `TestChangePasswordPayloadMatchesDLL`.
- [ ] **Step 3–5:** FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): change_pass`.

---

## Фаза 7. Опционы

### Task 27: `get_option_families`, `get_family_strikes`, `get_options`

PDF 3.35–3.37, 4.38–4.40. Обработчики `FUN_1801982a0`, `FUN_1801981e0`, `FUN_1801982e0`; запросы 149, 153, 150/151.

**Files:**
- Create: `native/session/options.go` + тест, `native/records/options.go` + тест, `native/model/option.go`, `native/client_options.go`

**Interfaces:**
- Produces: `OptionFamilies(ctx, underlying model.Symbol) ([]model.OptionFamily, error)`, `FamilyStrikes(ctx, family string) ([]model.Strike, error)`, `Options(ctx, model.OptionsRequest) ([]model.Option, error)`. Ошибка 4.40 — `*OptionsError{Code int; Message string}`.

- [ ] **Step 1: RE-1**; **RE-2** (опционы на RTS/Si на демо).
- [ ] **Step 2–5:** тесты по паре на команду → FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): options`.

---

## Фаза 8. РПС и РЕПО

### Task 28: `newrpsorder`, `newrepoorder`, `newmrepoorder`, `cancelnegdeal`, `newreport4`, `cancelreport4`; сделки и отчёты на исполнение

PDF 5.1–5.8. Обработчики `FUN_180198560`, `FUN_1801984e0`, `FUN_1801984a0`, `FUN_180197ee0`, `FUN_180198520`, `FUN_180197f60`; запросы 72 RQ_RPSREPO_SUBSCR, 82 RQ_GET_RPSREPO, 21/22 RQ_REPORT_INFO/RQ_GET_REPORT; транзакции — задача 15.

**Files:**
- Create: `native/records/rps.go` + тест, `native/model/rps.go`, `native/client_rps.go`

**Interfaces:**
- Consumes: `session.Transact` (задача 15).
- Produces: методы по одному на команду, события `model.RPSTradeForExecution` (5.7), `model.ExecutionReport` (5.8).

- [ ] **Step 1: RE-1**; только **RE-3** (на демо-счёте РПС, скорее всего, недоступны — проверить и записать).
- [ ] **Step 2–5:** тесты по паре на команду → FAIL → Implement → PASS.
- [ ] **Step 6: Commit** `feat(native): RPS and REPO`.

---

## Фаза 9. Интеграция

### Task 29: XML-коннектор целиком и gRPC-сервер без Wine

**Files:**
- Modify: `native/connector.go` (таблица команд → методы `Client`, рендер событий через `xmlcompat.Render`)
- Modify: `server/server.go` (`TC_BACKEND=native|dll`, по умолчанию `dll` на windows, `native` в остальных ОС)
- Create: `native/connector_matrix_test.go`

**Interfaces:**
- Consumes: все методы `Client` и `xmlcompat.Render` из задач 5–28.
- Produces: `server.Config.Backend string`.

- [ ] **Step 1: Write the failing test** `TestEveryDLLCommandIsHandled`: для каждого из 45 имён `dllCommands` `SendCommand` без соединения отвечает как DLL (`Cannot process…` или ответ команды без соединения). После `connect` на testserver ни одна не отвечает `not implemented`, кроме `set_csp` и `authenticate` — они нужны внешним криптопровайдерам, которые вне встроенного профиля. Их ответ берётся из RE-1 `FUN_180197d60`/`FUN_180197da0` и записывается в `docs/protocol/commands.md`.
- [ ] **Step 1b: Write the failing test** `TestServerErrorTexts`: тексты ошибок сервера и коннектора из PDF 4.41 приходят в `<result success="false"><message>`/`<error>` так же, как у DLL. Источник — оракул RE-3: ответы RQ_TRANSACT, RQ_HISTORY и RQ_PWD с кодами ошибок. Таблица «код → текст» записывается в `docs/protocol/errors.md`.
- [ ] **Step 2: Write the failing test** `TestServerRunsNativeBackend` в `server/`: `RunContext` с `Backend:"native"` и testserver; gRPC-клиент `SendCommand(<command id="server_status"/>)` получает `<result success="true"/>`.
- [ ] **Step 3: Run** — FAIL. **Step 4: Implement.** **Step 5: Run** `go test -race ./...` — PASS.
- [ ] **Step 6: RE-4:** `make server` без Wine на macOS (`TC_BACKEND=native go run .`), `examples/grpc-client` проходит `connect`→`get_securities`→`subscribe`→`disconnect` на демо.
- [ ] **Step 7: Commit** `feat(server): native backend without Wine`.

### Task 30: Матрица покрытия, длительный прогон, выпуск

`docs/ROADMAP-GO.md`, этап M7.

**Files:**
- Create: `docs/protocol/coverage.md`, `tools/native-soak/main.go`
- Modify: `docs/ROADMAP-GO.md`, `README.md`

**Interfaces:**
- Produces: таблица «команда/сообщение PDF → метод/событие Go → статус S/O/D → тест».

- [ ] **Step 1:** `docs/protocol/coverage.md`: каждая строка разделов 3, 4, 5 PDF с методом, событием, статусом, тестом. Пустых строк нет. Непокрытое — с причиной (`get_leverage_control` — нет в DLL 6.47).
- [ ] **Step 2: `tools/native-soak`:** торговая сессия целиком (10:00–23:50) с подписками на 20 бумаг. Раз в 5 мин в лог идут `Stats`, RSS и задержка опроса; два обрыва через `tcproxy`. Критерий: `UnknownRecords==0`, восстановления успешны, потребление памяти стабильно.
- [ ] **Step 3: README:** раздел «Нативный клиент» с примером `Dial`/`Subscribe`/`Run`.
- [ ] **Step 4: Commit** `docs: native client coverage matrix and soak results`.
