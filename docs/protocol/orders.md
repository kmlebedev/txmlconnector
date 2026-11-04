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
совпадает с клиентом сессии — иначе DLL молча отбрасывает запись (O). Подтверждённое соответствие
полей: `<orderno>` — `G` №20 тела (номер заявки от сервера), `<secid>`/`<board>`/`<seccode>` —
`W`/`W` + справочник, `<client>`/`<brokerref>` — строковые поля, `<buysell>` — байт (`B`/`S`),
`<status>` — байт статуса (1 → `watching`; полный enum — I), `<price>` — `L` №7 в единицах
10^-decimals (10000 → 100.0), `<yield>` — `L` №9 (555 → 5.55), `<quantity>` — `L` №10,
`<balance>`, `<conditionvalue>`, `<validafter>`, `<validbefore>`, `<accepttime>` — соответствующие
`L`/`W` (I: точные номера полей фиксированы экспериментом M6, но не для всех элементов).

**Фикстуры:** `order-new-inject.hex` (124 Б, обезличено: client-строки → `TESTCLIEN`, числовой
client id/orderno → синтетические) + `order-new-dll.xml` (ожидаемый XML; снят с DLL до маскирования).

## R_TRD (0x04\*) — D, не добито (I)

Размерная запись; обработчик `FUN_1803c5970` (`NEW_TRADE: …`), тело — `FUN_1803c6990`
(`ReadOneTrade`), 47 чтений: `W,W,G,B,G,G,L,B,Z20,L,W,L,G,G,G,G,Z12,L,L,L,Z12,L,L,L,G,G,G,L,L,L,
L,B,L,L,G,W,G,L`, затем условный блок от байта `+0x7a` (`B`, при ненулевом — доп. поля
`B,G,G,B,G,W,Z12`). Попытка инъекции тела из 39 полей: `NEW_TRADE: incorrect buffer` —
часть полей обязательна сверх этого набора; layout не добит (I), фикстуры `trade-*` пока нет.

## R_ORD_MODIFY (0x10\*) — D, предварительно (I)

`FUN_1803c3590` (`UpdateOrder: …`), размерная; чтения идут напрямую в этой функции (список в
`.re-run/re/decomp-orders.c`), ищет заявку по ID и применяет изменения; полный layout и oracle — I.

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
