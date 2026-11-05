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

## R_ORD_NEW (0x03\*) — D + O + S

Размерная запись; обработчик `FUN_1803c1b20` (лог `ReadOneOrder: …`). Тело: `L order_id`
(→ `<order transactionid="…">`), затем `ReadOrderBody` (`FUN_18039b390`). **Чтения зависят от
`W mask` после цены, от байта условия и от статуса** (D, разобрано 2026-11-05 для A8; прежняя
«линейная 52-чтения» схема была неверна и совпадала с O-фикстурой только случайно):

```
W secid, W board, G client_id, B buysell, B (+0xd, не в XML), L price, W mask,
[mask&1: L yield], L quantity, Z20 brokerref, B condition,
[condition≠0: L conditionvalue, L validafter, L validbefore, B within_pos(I), L (+0x13c; читается при PRO# ≥ 0x3f)],
[mask&2: G value], Z12 client, L accepttime, G orderno, B status, L time, L balance,
Z12 (+0x1e5), L (+0x3c), [mask&2: B, Z12 settlecode], [mask&4: L withdrawtime],
[mask&8: L], [mask&0x10: L, B], [mask&0x20: L, G],
[status 4 (OS_DENIED): B + блок GetResume — не восстановлен; status 6 (OS_REJECTED): Z512 → result],
[mask&0x40: L], [mask&0x80: B], B, B, B, B, B(+0x21a), [B(+0x21a)=='R': L, L, L, G, G, G], B, B,
[mask&0x100: W, W]
```

| Поле XML | Источник | Статус |
|---|---|---|
| `orderno`, `status`, `time`, `balance`, `withdrawtime`, `value`, `settlecode`, `price`, `quantity`, `buysell` | поля выше | O (мутационный оракул: каждое поле живой записи меняли по одному, 2026-11-05) |
| `client`, `union` | из хранилища клиентов по `G client_id`; строка Z12 в XML не идёт | O (мутация Z12 не меняет `<client>`) |
| `brokerref` | `Z20` (+0xe) | O (обе фикстуры) |
| `time`/`withdrawtime` | секунды: ≥ суток → `дд.мм.гггг чч:мм:сс` (секунды эпохи по серверному времени), иначе `чч:мм:сс`; `withdrawtime` = 0 → `0` | O; граница «сутки» — I |
| блок условия (`accepttime`, `conditionvalue`, `validafter`, `validbefore`) | выводится только при condition ≠ 0 | O (Bid — есть, None — нет) |
| `condition` | 0 → `None`, 1 → `Bid`, 3 → `Ask` | O для None/Bid |
| `within_pos` | B (+0x138) блока условия; без условия — `false` | I (согласуется с обеими O-формами) |
| `accruedint`, `hidden`, `maxcomission`, `yield` без mask&1 | `0.0`/`0`/`0.0`/`0.0` | O (константы в обеих формах) |

**Статусы (O, инъекция статусов 0–15 в живое тело):** 0 none, 1 watching, 2 disabled, 3 expired,
5 forwarding, 7 failed, 8 active, 9 cancelled, 10 removed, 11 matched, 12 refused, 13 wait,
14 inactive; прочие — `erorr: unknown status` (опечатка DLL). 4 и 6 — ветки OS_DENIED/OS_REJECTED
с дополнительными полями: тело без них DLL отбрасывает; имена denied/rejected — I. Заявка со
статусом 4 у `native` помечается `Short` и пропускается (блок GetResume не восстановлен).

**Загрузка (S, демо, A8):** заявки дня в конечных статусах приходят в снапшоте полными 0x03 (mask
0x116); DLL отдаёт их **одним** `<orders>` сразу после `<client>` и до `<positions>`. Раньше
`native` их пропускал (`Short`) — исправлено; колбэк нашей библиотеки на живой загрузке совпадает с
DLL побайтно. Фильтр по клиенту прежний: заявка чужого `client_id` отбрасывается (O).

**Оракул (O):** инъекция `order-new-inject.hex` даёт `order-new-dll.xml`; инъекция живой снятой
заявки — `order-final-cancelled-dll.xml`.

**Фикстуры:** `order-new-inject.hex` (обезличено: client-строки → `TESTCLIEN`, числовой client id →
4096). При обезличивании 2026-11-05 исправлено: первые три байта строки клиента Z12 (12 символов,
без нуля внутри) прежде были заменены на `020001` (с нулём) — теперь `TST`; то же в
`order-modify-inject.hex`. `order-short-cancelled-live.hex` — живая снятая заявка (S, client id → 4096,
логин → `TESTLOGI`), `order-final-cancelled-dll.xml` — её XML у DLL (client/union обезличены).

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

## Offset-таблица payload newstoporder/newcondorder (task-47, O: дельта-анализ)

Метод: 4 варианта `newstoporder` и 3 `newcondorder` с разными XML-значениями; payload’ы сняты
через tcproxy + fake reply type=14. Базовые payload’ы (client замаскирован) и их hex:

```
NS_base  act100 ord90  qty1 valbefore 05.11 18:00:
  14 00 00 00 1a 00 25 22 69 1a 42 03 11 00 00 00 00 02 71 02 82 32 02 10 00 00 02 53 07 00 00 10 00 00 00
NS_act110:
  14 00 00 00 1a 00 25 22 69 1a 42 03 11 00 00 00 00 82 af 02 82 32 02 10 00 00 02 53 07 00 00 10 00 00 00
NS_act200 ord190 qty7:
  14 00 00 00 1a 00 25 22 69 1a 42 03 11 00 00 00 00 02 e2 04 82 a3 04 70 00 00 02 53 07 00 00 10 00 00 00
NC_base qty1 price100 Bid290 val 05.11 10:00:
  01 00 00 1a 00 25 22 69 1a 42 00 02 71 02 10 00 01 82 14 07 04 3a c5 ae 06 04 42 cc ae 06 00 00 00 00 00 00 00 00
NC_condval291:
  01 00 00 1a 00 25 22 69 1a 42 00 02 71 02 10 00 01 c2 1a 07 04 3a c5 ae 06 04 42 cc ae 06 00 00 00 00 00 00 00 00
NC_qty7 price101:
  01 00 00 1a 00 25 22 69 1a 42 00 42 77 02 70 00 01 82 14 07 04 3a c5 ae 06 04 42 cc ae 06 00 00 00 00 00 00 00 00
```

**newstoporder (тип-байт `0x14`), подтверждённые смещения (0-based в payload):**

| Смещение | Поле | Кодировка (O) |
|---|---|---|
| 0 | тип транзакции | `B 0x14` (TRN_NEWSTOPORDER) |
| 1..2 | флаги/резерв | `B 00, B 00` |
| 3-4 | `secid` | `W` BE |
| 5-6 | `board` | `W` BE |
| 7-9 | `client_id` | packed `L` (108178 → `22 69 1a`) |
| 10 | `buysell` | `B` (`0x42`=B / `0x53`=S) |
| 11 | ? | `B 0x03` (в базовом) |
| 12 | ? | `B 0x11` (в базовом) |
| 13-16 | нули/резерв | — |
| **17** | **activationprice стоп-лосса** | packed `L`, цена ×100 (100→`02 71 02`=10000; 110→`82 af 02`=11000) |
| **20** | **orderprice стоп-лосса** | packed `L`, ×100 (90→`82 32 02`=9000; 190→`82 a3 04`=19000) |
| **23** | **quantity стоп-лосса** | packed `L` (1→`10`; 7→`70`) |
| 26-28 | activationprice тейк-профита | packed `L`, ×100 (300→`02 53 07`=30000) |
| 28+ | остальные поля тейк-профита/резерв | I |

`validbefore` при изменении (05.11→06.11) payload НЕ изменился — вероятно, не передаётся в этой
транзакции (I; проверить на S). `validafter` в теле не варьировался (I).

**newcondorder (базовый тип `0x01`, как TRN_NEWORDER) — подтверждённые смещения:**

| Смещение | Поле | Кодировка (O) |
|---|---|---|
| 0 | тип | `B 0x01` |
| 1 | флаг | `B 00` |
| 2-3 | `secid` | `W` BE (26) |
| 4-5 | `board` | `W` BE (37) |
| 6-8 | `client_id` | packed `L` |
| 9 | `buysell` | `B` (`0x42`) |
| 10 | флаг | `B 00` |
| **11** | **price** | packed `L`, ×100 (100→`02 71 02`=10000; 101→`42 77 02`=10100) |
| **14** | **quantity** | packed `L` (1→`10`; 7→`70`) |
| 15 | флаг | `B 00` |
| **16** | **cond_type** | `B`: Bid=`01`, BidOrLast=`02`, AskOrLast=`04` (Last отклоняется) |
| **17** | **cond_value** | packed `L`, ×100 (290→`82 14 07`=29000; 291→`c2 1a 07`=29100) |
| 20-24 | validafter | packed `L`, unix-секунды (05.11.2026 10:00 → `04 3a c5 ae 06`) |
| 25-29 | validbefore | packed `L`, unix-секунды (`04 42 cc ae 06`) |
| 30+ | нули/резерв | — |

RE-3 (дельта-анализ, fake reply type=14): `cond_type` кодируется в байте 16:
Bid=0x01, BidOrLast=0x02, AskOrLast=0x04 (Last отклоняется DLL: «Incorrect condition type.»;
Time требует другого типа значения — I). 

**Лег-зависимые флаги newstoporder (RE-3):** SL-only — 27 Б, флаги байт 11/12 = `00 01`;
TP-only — 26 Б, `03 10`; обе ноги — 35 Б, `03 11`; validafter/validbefore на флаги не влияют.
Точные смещения takeprofit/condition-структур — I.EOF
echo ok

## S: демо-цикл neworder → moveorder → makeordown → cancelorder (2026-11-05)

Живой прогон на **демо-сервере** (только `~/re/demo.env`, 1 лот SBER, цена далеко от рынка,
немедленная отмена; на проде торговых команд не было). Фикстуры —
`native/records/testdata/transact-{neworder,cancelorder,moveorder}-live.hex` + `demo-trade-repro.md`.

### neworder (S)

- Payload 24 Б (client обезличен): `B 01 `(TRN_NEWORDER)`, B 00, W secid, W board, G client,
  B buysell('B'), B 00, L price(packed), L quantity(packed), 9 нулевых байт`. Пример:
  `0100001a0025020001420062f80610000000000000000000` (secid=26, board=37, price=28550=285.50, qty=1).
- Reply type=14: `B status(0), L transactionid, L server_time, B 00` (**S**: `00 a1 05 94 73 c5 ae
  06 00` → transactionid=90).
- Ценовой коридор сервера: цена ниже минимума отклоняется, заявка сразу `status="removed"`,
  `<result>(579)Для выбранного финансового инструмента цена должна быть не меньше 284.62</result>`
  (**S**; минимум плавающий: во втором прогоне 284.64).
- transactionid выдаёт сервер; наблюдённые значения 10, 20, 40, 60, 80, 90 — по-видимому,
  `10 × число успешных транзакций клиента` (**I**, гипотеза).

### cancelorder (S)

- Payload 5 Б: `B 02` (TRN_CANCELORDER), `B 00`, `L transactionid` (packed), `B 00`;
  пример `02 00 a1 05 00`.
- Reply: `B status(0)` (len=1); колбэк `<order … status="cancelled"><result>{42} Заявка
  снята.</result></order>` (**S**).
- Отмена работает и в **новой** сессии (DLL передаёт id как есть); для несуществующей транзакции
  сервер отвечает `[151]Объект операции не найден` (**S**; формат с квадратными скобками — как у
  серверных текстов).

### moveorder (D + O, успешный payload не снят — I)

- D: `FUN_180198420` → `FUN_1800dbb10` → `FUN_1800dc190`: `transactionid` — дочерний
  элемент/атрибут (`FUN_18040c140`), число; поиск транзакции в сторе (`FUN_18025d1e0`);
  нет — `Wrong transactionid value.`; `price` — элемент, число; `moveflag` — элемент, число
  **0..3**, иначе `Wrong moveflag value.`; для `moveflag` 1/2 обязателен `quantity`; затем
  `FUN_18037a7d0(conn, 0x13, txid, errbuf)` — отправка/ошибка в errbuf.
- Payload (снят для уже снятых транзакций; сервер ответил `[151]`): `B 02, B 01, L transactionid,
  B 00` — например `02 01 41 01 00` (txid=20) и `02 01 81 02 00` (txid=40).
- O: для **активной** заявки, созданной в этой же сессии (txid=90, moveflag=0, price=286), DLL
  отвечает `{32} Недопустимое значение параметра 'TransactionID' для данной транзакции` и payload
  НЕ шлёт (то же для moveflag=2/3) — **I**: условие успешной валидации в `FUN_18037a7d0` не
  восстановлено (вероятно, moveorder применим только к «своим условным» заявкам, создаваемым
  makeordown — гипотеза).

### makeordown (D + O, payload не снят — I)

- D: `FUN_1801983a0` → `FUN_1800efda0`: `transactionid` — **атрибут** (`FUN_18040cf20`),
  не элемент; ищет транзакцию (`FUN_18025d1e0`), условия `iVar1 != 0 && local_11e != 0 &&
  local_a0 == 1`, затем транзакция RQ_MAKE_COND_OWN (112). Ошибка при несоответствии:
  `Cannot make specified transaction_id an own conditional order` (**D + S**).
- O: `<command id="makeordown" transactionid="90"/>` на активную заявку → эта ошибка;
  payload RQ_MAKE_COND_OWN не снят — **I**.

### Итог по T19/task-44

- neworder/cancelorder — payload'ы сняты (S) ✔; moveorder — формат payload снят (S), но валидный
  сценарий (успешный move активной заявки) и makeordown остаются **I**: нужен либо разбор
  `FUN_18037a7d0` (условия {32}), либо серия с makeordown-совместимой транзакцией.
- Активных заявок не оставлено: все созданные заявки сняты/отклонены.


## T19 Go implementation (S payload, 2026-11-05)

- `Client.MoveOrder`: body `B 01, packed L transactionid, B 00`, TRN 0x02; full RQ_TRANSACT payload
  `02 01 41 01 00` (txid 20) matches `transact-moveorder-live.hex` byte-for-byte (S).
- Validations (D, exact texts): transactionid element/attr and >0 (`Wrong transactionid value.`),
  price (`Price not found.` / `Price have to be a number.`), moveflag 0..3
  (`Cannot find moveflag.` / `Wrong moveflag value.`), quantity required for 1/2
  (`Cannot find quantity.` / `Quantity have to be a number.`).
- Successful active-order move remains **I**: the oracle returns `{32}` for an active order; the S
  payload was captured only for already-cancelled transactions, so the Go command sends it and relies
  on the server reply/order update.
- `Connector` `moveorder` parses the XML elements/attrs; `makeordown` parses `transactionid` as an
  **attribute** and returns the explicit `RQ_MAKE_COND_OWN payload is not recovered (I)` error —
  the command is processed, not "not implemented".

- Task-67 live-load fix: cancelled/removed/denied/rejected R_ORD_NEW bodies are shorter than the linear 52-read scheme; `ParseOrderNew` marks them `Short` and the event decoder skips them (I) instead of failing the load. R_RES 0x0f parsing added per RE-2 task-68.


## makeordown: RQ_MAKE_COND_OWN (112) — D + O (2026-11-05, закрывает I)

- **D** (`FUN_1800efda0` → `FUN_1803861e0`): `transactionid` читается только из **атрибута**; ноль,
  отсутствие, нечисловое значение и неизвестная транзакция дают один текст
  `Cannot make specified transaction_id an own conditional order`. Запрос — **не** RQ_TRANSACT, а
  отдельный тип `0x70` (112) с телом `L transactionid` (упакованное). Ответ — C-строка до 0x200:
  пустая — успех, непустая — текст ошибки DLL.
- **Проверки DLL перед отправкой (D):** транзакция найдена в хранилище `+0x4f0` (`FUN_18025d1e0`
  копирует объект заявки), байт `+0x12a` (тип условия, поле №11 R_ORD_NEW) ≠ 0 и байт `+0x1a8` == 1.
- **O** (replay `r14` + DLL, инъекция одной записи 0x03 из `order-new-inject.hex` — условная заявка
  `watching`): `makeordown transactionid="1"` → `<result success="true"/>`, C>S `type=112 len=1 10`
  (фикстура `makecondown-payload.hex`); ответ проигрывателя пустой. `transactionid="2"` (нет в
  хранилище) → текст отказа, запрос не уходит. После записи 0x10 (заявка отменена, `disabled`) —
  тоже отказ.
- **Go:** `session.RqMakeCondOwn`, `Conn.MakeCondOwn`, `Client.MakeOrdOwn`. Локальная проверка —
  O-эквивалент: известная заявка с условием в статусе `watching`. Что именно хранит `+0x1a8` и
  точная форма `<result>` при непустом ответе сервера — **I**. Нативный коннектор на том же
  проигрывании дал те же два ответа и тот же payload (O).
- Живой S на демо не снимался: нужна условная заявка (торговая команда, отдельное согласие).
