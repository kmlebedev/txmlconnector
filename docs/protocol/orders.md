# RQ_TRANSACT (14): запрос, ответ и оракульные фикстуры (Task 15)

Дата: 2026-11-04. Источник: DLL `txmlconnector64-6.47.2.26.4.dll`; RE-1 `FUN_18039f490`
(общий построитель/разборщик RQ_TRANSACT) и RE-3 «оракул» — `tools/replay-server` с записанной
сессией (`.re-run/r14`, вне git): DLL под Wine подключается ТОЛЬКО к replay-server на 127.0.0.1,
в копию расшифрованного дампа добавлен S>C кадр type=14 с сконструированным ответом; harness шлёт
DLL XML `neworder`. Демо/реальный сервер не использовался, `change_pass` и иные операции не
отправлялись. Статусы: **O** — оракул DLL, **D** — декомпилятор, **S** — живой сервер, **I** —
вывод.

## XML-команды DLL, порождающие RQ_TRANSACT (O)

`neworder` (проверено оракулом; остальные торговые команды используют тот же обменник).
Минимальный набор элементов, который DLL требует (порядок сообщений валидации по мере добавления):
1) `secid` → без него: `Не указан элемент "secid"`;
2) `client` и/или `union` → `Должен быть указан клиент и/или юнион`;
3) `price` → `Price not found.`;
4) далее `quantity`, `buysell`, `unfilled` (с ними DLL формирует RQ_TRANSACT и ждёт ответ).
Рабочий пример (client/union заменены): `<command id="neworder"><secid>26</secid><client>TESTCLIENT</client><price>100</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>`.
Номер транзакции/orderno присваивает **DLL**, не клиент.

## Формат ответа RQ_TRANSACT (O)

Ответ — прямой reply-кадр type=14 (не RQ_MAIN). Payload начинается с байта кода результата
(`RSM_OK` и др.); код 0 — успех, и тогда дальше идут packed-L значения:

| Payload (hex) | XML DLL | Смысл |
|---|---|---|
| `00 10 00 10` | `<result success="true" transactionid="1"/>` | успех; первый packed-L после кода = transactionid |
| `00 20 00 10` | `<result success="true" transactionid="2"/>` | то же, transactionid=2 (подтверждает, что это первое L) |
| `01` | `<result success="false"><message>Клиент не допущен к торгам.</message></result>` | код 1 → текст из таблицы кодов DLL |
| пусто (len=0) | `<result success="false"><message>Unknown error</message></result>` + лог `RQ_TRANSACT ... return incorrect buffer` | ответ без данных |

Коды ошибок и тексты берутся из таблицы DLL (`RSM_*`): код 1 — «Клиент не допущен к торгам»;
полная таблица не снята — I. Хвостовые packed-L успешного ответа (`00 10` в примере) —
вероятно orderno/служебные поля; их точный смысл — I.

## Правила (обязательны для native-клиента)

- RQ_TRANSACT **не повторять автоматически**: каждая транзакция одноразовая.
- Неизвестный/непонятый исход (пустой ответ, таймаут, обрыв) — **ошибка** (`ErrOutcomeUnknown`),
  а не успех и не повтор.
- `transactionid` берётся из ответа сервера; DLL сверяет его со своей транзакцией.
- Реальная «тишина» (сервер не ответил) в оракуле не воспроизводима: replay всегда отвечает.
  По логу DLL (`Request timeout 20000 ms`) известен сам таймаут; поведение после него — I.

## RQ_TRNS_IN_PROGRESS (156) — D

Запрос пустой. Ответ (`FUN_1803de200`): поток записей до неудачного чтения L; поля записи по
порядку: `L, L, B, G, Z12, B, Z25, B, L, Z15, Z15`; запись сопоставляется с бумагой/клиентом и
кладётся в очередь «сделки в процессе». Точные имена полей и XML-эффект не установлены — I.

## RQ_ENFORCE_TRN_RELEASE (157) — D

Запрос (`FUN_1803ded20`): `L` — младшие 32 бита номера, `B` — старший байт; затем обмен и лог
`TRN_RELEASE: <n> rsp:` . Формат ответа не разобран (предположительно B-статус) — I.

## Фикстуры (native/records/testdata)

| Файл | Что внутри |
|---|---|
| `transact-success-reply.hex` | payload успешного ответа `00100010` |
| `transact-success-dll.xml` | ожидаемый XML DLL: `<result success="true" transactionid="1"/>` |
| `transact-error-reply.hex` | payload ошибки `01` |
| `transact-error-dll.xml` | ожидаемый XML DLL с текстом «Клиент не допущен к торгам.» |
| `transact-empty-reply.hex` | пустой ответ (0 байт) |
| `transact-empty-dll.xml` | `<result success="false"><message>Unknown error</message></result>` |

Обезличивание: в ответах нет client/union/orderno (только transactionid=1 и текст ошибки);
реальный клиент оракульного прогона в фикстуры и документацию не попадает (маскирован
TESTCLIENT). Сырые дампы/логи — только в `.re-run/`.

## Не проверено

- Полная таблица кодов `RSM_*` → тексты, семантика хвостовых L успешного ответа.
- Ответы для `cancelorder`/stop/cond/multileg и разбор ответов 156/157.
- Поведение при реальном таймауте и обрыве во время ожидания RQ_TRANSACT.
- Живой демо-сервер для торговых команд не использовался (только оракул) — намеренно.

## RQ_TRANSACT: payload neworder / cancelorder (O)

Снято оракулом через цепочку DLL → `tcproxy` → `replay-server` (дамп .re-run/r14 с
сконструированными reply type=14); реальная биржа не участвовала, `change_pass` не отправлялся.

**neworder (24 байта)** — `B 01` (TRN_NEWORDER), `B 00`, `W secid`, `W board`, `G client_id`
(числовой id клиента из каталога), `B buysell` (`B`/`S`), `B 00`, `L price` (цена в единицах
10^-decimals; 100.00 → 10000), `L quantity`, далее 9 нулевых байт (флаги unfilled/служебные — I).
Пример с замаскированным client_id (G 108178 → dummy 3-байтовый 4096):
`0100001a0025020001420002710210000000000000000000` — фикстура
`transact-neworder-payload.hex`.

**cancelorder (4 байта)** — `B 02` (TRN_CANCELORDER), `B 01`, `L transactionid`, `B 00`;
пример `02011000` — фикстура `transact-cancelorder-payload.hex`. XML-команда:
`<command id="cancelorder"><transactionid>N</transactionid></command>` (без неё DLL отвечает
«You have to specify transactionid.» — O).

**Правило:** payload содержит числовой id клиента (секрет) — в фикстурах он заменён dummy-значением
той же packed-длины; тест должен собирать payload из своего каталога, а не из фикстуры байт-в-байт.

## R_ORD_NEW (0x03\*) — D + O

Размерная запись; обработчик `FUN_1803c1b20` (лог `ReadOneOrder: …`). Тело: `L order_id`
(→ `<order transactionid="…">`), затем тело заявки — `FUN_18039b390` (`ReadOrderBody`), 52 чтения
в порядке (D): `W,W,G,B,B,L,W,L,L,Z20,B,L,L,L,B,L,G,Z12,L,G,B,L,L,Z12,L,B,Z12,L,L,L,B,L,G,B,
Z512,L,B,B,B,B,B,L,L,L,G,G,G,B,B,W,W`. Часть чтений условная (по статусу: ветки
`OS_DENIED`/`OS_REJECTED` читают дополнительные поля — D).

**Oracle (O):** инъекция сконструированной записи 0x03 через `replay-server` даёт колбэк
`<orders><order transactionid="…">…</order></orders>`; форма и порядок элементов сняты с DLL
(см. `order-new-dll.xml`). Заявка отдаётся только если её числовой client id (третье `G` тела)
совпадает с клиентом сессии — иначе DLL молча отбрасывает запись (O). **Точная таблица полей тела 0x03 (D+O; номера — после `L order_id`):**

| № | Тип | Смещение | XML-элемент (O) |
|---|---|---|---|
| 1 | W | +0x2 | `<secid>` |
| 2 | W | +0x4 | `<board>` |
| 3 | G | +0x8 | служебный id |
| 4 | B | +0xc | `<buysell>` |
| 5 | B | +0xd | `<status>` (1 → watching; в 0x10 — новый статус) |
| 6 | L | +0x24 | `<price>` = v/100 (10000 → 100.0) |
| 7 | W | локальная | не в XML |
| 8 | L | +0x28 | `<yield>` = v/100 (555 → 5.55) |
| 9 | L | +0x30 | `<quantity>` (333) |
| 10 | Z20 | +0xe | `<seccode>` (из справочника) |
| 11–17 | B/L/G | +0x12a, +0x12c, +0x130, +0x134, +0x138, +0x13c, +0xf8 | `<conditionvalue>` (12: v/100 → 0.07), `<validafter>` (13: сек, 8 → 00:00:08), `<validbefore>` (14: 9 → 00:00:09), остальное — I |
| 18 | Z12 | +0x1d8 | `<client>` |
| 19 | L | +0x1ac | `<accepttime>` (сек: 999 → 00:16:39) |
| 20 | G | +0x1a0 | `<orderno>` (4242) |
| 21 | B | +0x1a8 | — |
| **22** | **L** | **+0x1c0** | **`<time>` (сек: 11 → 00:00:11)** |
| **23** | **L** | локальная (`local_3b0`) | **`<balance>` (1)** |
| 24 | Z12 | +0x1e5 | `<brokerref>`/`<settlecode>` |
| 25–52 | … | +0x3c, +0x41, +0x62, +0x1c4, +0x1b4, +0x14c, +0x150, +0x154, +0x1b8, +0x1f2, Z512 лок., +0x38, +0x161, +0x215…+0x21a, +0xc4, +0xa4, +0xa0, +0x90, +0x98, +0x220, +0x228, +0x229, +0x22a, +0x22c | остальные (I) |

**O-проверка:** один 0x03-рекорд (байты `order-new-inject.hex`) даёт у DLL `time=00:00:11`,
`balance=1`; запись 0x03+0x10(mask=1) меняет **только** `<status>` (watching → disabled) и
`<result>` («{37} Заявка отменена»), time/balance не трогает. Пара `order-new-inject.hex`/
`order-new-dll.xml` была несогласована (XML снят с другой инъекции: time=1/balance=11) — **XML
исправлен**; пара `order-modify-*` верна.

**Фикстуры:** `order-new-inject.hex` (124 Б, обезличено: client-строки → `TESTCLIEN`, числовой
client id/orderno → синтетические) + `order-new-dll.xml` (ожидаемый XML; снят с DLL до маскирования).

## R_TRD (0x04\*) — D + O

Размерная запись; обработчик `FUN_1803c5970` (`NEW_TRADE: …`), тело — `FUN_1803c6990`
(`ReadOneTrade`). Формат (D): `W secid, W board, G client_id, B tradetype, G tradeno, G orderno,
L time, B ? , Z20 seccode, L price, W mask`, далее поля по маске (`mask & 1`: L/G/G;
`mask & 2`: L,L,L; `mask & 4`: L,L,L,G,G,G,L,L,L,L,B; `mask & 8`: L,L,G; `mask & 0x10`: W,G;
`mask & 0x20`: L; `mask & 0x40`: B), затем безусловные `G quantity, G value, G comission,
Z12 client, Z12 settlecode, B, B, B, D, B, G`, и опционально `W`/`Z12` при `mask & 0x80`/`0x100`.
`G client_id` — фильтр: сделка чужого клиента в `<trades>` не отдаётся (O).

**Oracle (O, mask=0):** `<trades><trade><secid>…</secid><tradeno>2222</tradeno><orderno>3333</orderno>
<board>TQBR</board><seccode>SBER</seccode><client>…</client><buysell></buysell><union></union>
<time>02:46:40</time><brokerref>…</brokerref><value>55.55</value><comission>66.66</comission>
<price>0.01</price><quantity>4444</quantity><items>4444</items><yield>0.0</yield>
<currentpos>-4444</currentpos><accruedint>0.0</accruedint><tradetype>B</tradetype><settlecode>…</settlecode>
<bypass>1</bypass></trade></trades>`. Подтверждено: `<tradeno>`=G(5-е поле), `<orderno>`=G(6-е),
`<value>`=G/100 (5555→55.55), `<comission>`=G/100 (6666→66.66), `<quantity>/<items>/<currentpos>`
=G (4444, currentpos=-4444), `<time>`=L (10000→02:46:40), `<price>`=L (1→0.01), `<tradetype>`=B.

**Фикстуры:** `trade-inject.hex` (71 Б; client-строки → `TESTCLIEN`, числовой client id → 4096,
остальные значения синтетические) + `trade-dll.xml` (снят с DLL до маскирования).

## R_ORD_MODIFY (0x10\*) — D + O

Размерная запись; обработчик `FUN_1803c3590` (`UpdateOrder: …`). Тело: `L order_id` (должен быть
найден в словаре заявок, иначе лог `!!!NOT FOUND!!!`), затем **uint32 BE mask** (`FUN_1801bdc50`,
не packed), затем поля по маске: `mask&1` → `B` статус; `mask&2` → `G`; `mask&4` → `L`;
`mask&8` → `L`; `mask&0x10` → `L`; `mask&0x20` → `L`; `mask&0x40` → `Z12`; `mask&0x80` → `L`;
`mask&0x100` → `L`; `mask&0x200` → `L`; `mask&0x400` → особый блок (`B`/`Z512` и далее по статусу).
После применения обновлённая заявка вставляется в `<orders>` (как 0x03).

**Oracle (O):** инъекция в одном RQ_MAIN двух записей — 0x03 (создаёт заявку, transactionid=1) и
затем 0x10 (`L 1, mask=0x00000001, B=2`) — даёт ОДИН итоговый колбэк `<orders>` с обновлённым
статусом: `<status>disabled</status>`, `<result>{37} Заявка отменена</result>`; промежуточный
«watching» не отдаётся (каллбэк после применения). Это и есть семантика обновления заявки для
`<orders>`.

**Фикстуры:** `order-modify-inject.hex` (133 Б: последовательность 0x03+0x10, client-строки
→ `TESTCLIEN`, числовой client id → 4096) + `order-modify-dll.xml` (ожидаемый итоговый `<orders>`;
снят с DLL до маскирования).

## XML-формы <orders>/<trades> — D (обнаружено)

Обработчики вставки заявок/сделок отдают колбэки через большие генераторы `FUN_1800f7b40`:
литералы `<orders>`/`</orders>` — `0x180836cf8`/`0x180836d08`, `<trades>`/`</trades>` —
`0x180836da0`/`0x180836db0`, `<ustrades>`/`</ustrades>` — `0x180836e58`/`0x180836e68`;
элемент заявки — `<order transactionid="…">` (`0x18082fbf0`), внутри — `<orderno>`,
`<ordertype>`, `<orderprice>` и др. (строки `0x18082fc50`, `0x180831018`, `0x180830520`).
Полный порядок элементов/атрибутов — I (строитель в огромном генераторе; нужен отдельный asm-разбор).

## Воспроизведение (без секретов)

```sh
# копия расшифрованного дампа + сконструированный ответ
cp .re-run/r14/decode-full.txt /tmp/dump14.txt
echo "9999.000 1 S>C #9001 flags=0x82 type=14 len=4 00100010" >> /tmp/dump14.txt
.re-run/replay-server-bin -dump /tmp/dump14.txt -listen 127.0.0.1:39452 &
RE_WORKDIR=$PWD/.re-run/t24-success tools/re/run_dll.sh 4s "$CONNECT" sleep:4s "$NEWORDER" sleep:3s "$DISCONNECT"
```

## R_STOP (0x54*) — D + O (фикстура снята)

Запись размерная (BE16). Обработчик `FUN_1803c2850` (лог `ReadOneStop: …`): тело =
`L stop_order_id`, затем `FUN_18039d3e0` (`ReadStopBody`), начинающийся с **`L mask`**:

```
L stop_order_id
L mask
  mask & 1     -> B status (?), валидация кодов статуса (0/10..14/0x18)
  mask & 2     -> L (+0xdc)
  mask & 4     -> L (+0xe0)
  mask & 8     -> L (+0xe4)
  mask & 0x10  -> Z12 (+0xfd)
  mask & 0x20  -> G (+0xc0)
  mask & 0x40  -> G (+0xc8)
  mask & 0x80  -> блок статуса: status==4 (OS_DENIED) B+0x10a + GetResume; status==6 (OS_REJECTED) Z512
  mask & 0x100 -> W secid (+6)
  mask & 0x200 -> W board (+8)
  mask & 0x400 -> L client_id (+0xc)
  mask & 0x800 -> B buysell (+0x11; валидируется как B/S)
  mask & 0x1000 -> W (local_1a4)
  mask & 0x2000 -> L (+0x78)
  mask & 0x4000 -> Z12 (+0xf0)
  mask & 0x8000 -> L (+0xd4)
  mask & 0x10000 -> G (+0x80) … далее старшие биты для остальных полей (I)
```

Если после чтения secid == -1 или client < 0 — запись игнорируется с логом
`New/Update Stop <id> ignored: Sec=…, Cl=…`. При успехе — вставка/обновление через
`FUN_18024cf70` и колбэк.

**XML (O): конверт — `<orders>`**, внутри — `<stoporder transactionid="…">`:

```xml
<orders><stoporder transactionid="1"><secid>26</secid><union></union><board>TQBR</board>
<seccode>SBER</seccode><client>TESTCLIEN</client><buysell>B</buysell><author>TESTLOGIN</author>
<status>none</status><withdrawtime>0</withdrawtime>
<result>{50} Статус заявки не определен.</result></stoporder></orders>
```

Прочие элементы (литералы DLL, порядок): `<canceller>`, `<alltradeno>`, `<validafter>`?,
`<validbefore>`, `<accepttime>`, `<linkedorderno>`, `<expdate>`, `<stoploss usecredit="…">`
(`<activationprice>`, `<guardtime>`, `<brokerref>`, `<quantity>`, `<bymarket/>`/`<orderprice>`,
`<correction>`), `<takeprofit usecredit="…">` (`<activationprice>`, `<guardtime>`, `<brokerref>`,
`<quantity>`, `<extremum>`, `<level>`, `<correction>`, `<bymarket/>`, `<guardspread>`), `<result>`.
Точные условия вывода элементов по маске — I.

**Фикстура:** `native/records/testdata/stoporder-inject.hex` (14 Б: tag 0x54 sized, `L 1`,
`L mask=0x0F00`, `W 26`, `W 37`, `L client=4096`, `B 'B'`) + `stoporder-dll.xml` (O;
client→TESTCLIEN, author/login→TESTLOGIN).

## Команды стоп-/условных заявок (Task 18) — O-валидация, payloads в работе

- `newstoporder`/`newcondorder`: требуют `<secid>` (как neworder), затем `client`/`union`;
  `newstoporder` далее — «activation price» стоп-лосса вложенным элементом
  (`<stoploss><activationprice>…</activationprice></stoploss>`; плоский `<stoploss>100</stoploss>`
  не принимается); `newcondorder` — параметр `validafter` и `cond_type`/`condition type`.
- `cancelstoporder`/`moveorder`/`makeordown`: требуют `transactionid`, проверяемый по внутренней
  таблице транзакций DLL.
- RQ_TRANSACT-payloads этих команд пока не сняты: нужен валидный XML + fake success reply type=14
  (схема RE-3 как в task-24) — следующий шаг; в фикстурах будут обезличены client/union/orderno.

## RQ_TRANSACT payloads команд Task 18 (O, oracle + tcproxy)

Снято цепочкой DLL → tcproxy → replay-server (127.0.0.1) с fake success reply type=14; реальных
торговых команд не отправлялось, client id/строки замаскированы (22 69 1a → 02 00 01,
virt/9993 → TESTCLIEN, длины сохранены).

- **newstoporder** (35 Б): `14 00 00 1a 00 25 02 00 01 42 03 11 0000 0000 02 71 02 82 32 02 10 00
  00 02 53 07 00 00 10 00 00 00`. Начало как у neworder (`B тип 0x14`, `B 00`, `W secid`,
  `W board`, `G client`, `B buysell`), затем stop-поля (цена/условия стоп-лосса и тейк-профита;
  точные смещения — I). Фикстура: `transact-newstoporder-payload.hex`.
- **cancelstoporder** (2 Б): `17 10` = `B 0x17` (тип TRN_CANCELSTOPORDER) + `L transactionid=1`.
  Фикстура: `transact-cancelstoporder-payload.hex`.
- **newcondorder** (39 Б): `01 00 00 1a 00 25 02 00 01 42 00 02 71 02 10 00 01 82 14 07 04 3a c5
  ae 06 04 42 cc ae 06 00 00 00 00 00 00 00 00`. Первый байт `0x01` — как у TRN_NEWORDER:
  условная заявка уходит базовым типом neworder с дополнительными cond-полями (тип условия,
  значение, validafter/validbefore — I по смещениям). Фикстура:
  `transact-newcondorder-payload.hex`.

Подтверждена успешная валидация XML DLL (fake reply: `<result success="true" transactionid="…"/>`
для newstoporder/newcondorder; cancelstoporder — success без id). Точная семантика каждого байта
в stop-полях — I, нужна одна итерация с записью R_STOP-обновления (следующий шаг).EOF
echo ok

## moveorder / makeordown (Task 19) — D + O-валидация, payloads не сняты (блокер)

**D:** `moveorder` — обработчик `FUN_180198420`; `makeordown` — `FUN_1801983a0`;
`RQ_MAKE_COND_OWN` (112) — обменник `FUN_1803861e0` (строки `RQ_MAKE_COND_OWN …`).

**O (реплейный DLL, fake reply type=14):**
- `moveorder`: требует `transactionid` **и** элемент `moveflag` (без него — «Cannot find moveflag.»);
  с несуществующим/не-активным id — «{32} Недопустимое значение параметра 'TransactionID' для
  данной транзакции». Даже после fake-success `neworder` (transactionid=1) moveorder с id=1 даёт
  ошибку {32}: по-видимому, DLL проверяет наличие **активной заявки в своём сторе**, а наш fake
  reply не создаёт полноценную заявку — для снятия payload нужен ответ, порождающий активный ордер
  (или живая S-сессия). Payload `transact-moveorder-payload.hex` пока НЕ снят.
- `makeordown`: команда в этой конфигурации не доходит до RQ_MAKE_COND_OWN (нет ни результата,
  ни C>S type=112 в захвате; harness завершает серию после предыдущих транзакций) — вероятно,
  требует иного набора полей/не поддержана DLL. Формат/наличие — **I/не подтверждено**;
  `RQ_MAKE_COND_OWN` (112): запрос/ответ — D-обменник известен, layout не восстановлен.

**Итог:** task-44 требует либо S-сессии (утром), либо более полного fake-ответа neworder;
фикстур moveorder/makeordown пока нет. Зафиксировано честно; после утренней S-съёмки — добрать.EOF
echo ok

**Доп. oracle-попытка (O):** инъекция RQ_MAIN `0x03`-заявки (order-new-inject, transactionid=1) +
fake success reply type=14, затем `moveorder transactionid=1, moveflag=0` — DLL всё равно отвечает
`{32} Недопустимое значение параметра TransactionID для данной транзакции`: таблица транзакций
moveorder не заполняется из RQ_MAIN-записей. Без живой торговой сессии payload moveorder не снять;
подтверждено с двух заходов.
