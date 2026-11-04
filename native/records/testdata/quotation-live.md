# R_QUOTATION (0x06) и `<quotations>`: живые фикстуры (задача 9)

Дата захвата: 2026-11-04 (вечерняя сессия), демо-сервер Finam, DLL
`txmlconnector64-6.47.2.26.4.dll` под Wine 11.21 через `tools/tcproxy`.
Сырьё (вне git): `.re-run/r9/` (основной прогон), `.re-run/r8/` (SBER), `.re-run/cap/quotation-live2.cap`.

## Прогоны

| Run | Что делали | Что подтвердили |
|---|---|---|
| r8 | `connect`; через 3 с `subscribe` с 4 элементами `<quotations>` (SBER 26, SiZ6 20583, HYDR 2, HYDR 8326); затем `subscribe` alltrades+quotations для SBER; `unsubscribe`; `disconnect` | маски RQ_SUBSCRIBE: `0x01` (quotations), `0x05` (alltrades+quotations), `0x00` (unsubscribe); SBER-котировка (полная маска) и колбэк `<quotations>` |
| r9 | `connect`; по одной `subscribe`-команде на бумагу: SBER 26 (успела до конца загрузки — success=false), SiZ6 20583, RIZ6 20573, GAZP 379, HYDR 2 (TQBR), HYDR 8326 (MCT); 75 с ожидания; `unsubscribe`; `disconnect` | 16 записей `0x06` и 16 колбэков `<quotations>` (SiZ6, RIZ6, GAZP, HYDR TQBR, HYDR MCT — 12 обновлений); фикстуры ниже |

Подписка: `<command id="subscribe"><quotations><secid>26</secid></quotations></command>`.
DLL принимает только **первый** элемент `<quotations>` из одной команды: в r8 команда с четырьмя
элементами дала success=true, но RQ_SUBSCRIBE/RQ_SEC_SPEC ушли только для SBER (**S**; для
нескольких бумаг — отдельные команды, как в r9).

## Файлы

| Файл | Что внутри |
|---|---|
| `quotation-live.hex` | hex: 429 байт — 16 записей `R_QUOTATION` (0x06) подряд, байт-в-байт из RQ_MAIN-ответов r9 |
| `quotations-dll.xml` | 16 колбэков `<quotations>…</quotations>`, по одному на строку (переводы строк внутри XML заменены пробелами; содержимое и значения — из DLL) |

Порядок записей и колбэков совпадает 1:1 (по `secid`), проверено для всех 16 событий.

## Формат записи (D, S)

`0x06` — **безразмерная** запись (длину задаёт маска):

```
W secid; W board; G mask (64-бит, wide packed FUN_1801bdb90);
далее по одному L-значению (packed int32) для каждого установленного бита mask,
в порядке возрастания номера бита
```

Обработчик `FUN_1803bc2f0`: `FUN_1801bd5d0` (secid), `FUN_1801bd380` (board), `FUN_1801bdb90`
(mask), `FUN_1801bd7b0` (значения); число полей `FUN_180263d60`, номер поля → слот
`FUN_180255880`, сравнение с прошлым состоянием и признак изменения `FUN_18026c4c0`; при
изменении — событие в очередь `FUN_1802aad00(ctx+0x100, secid|0xa0000)`. XML строит большой
генератор `FUN_1800f7b40` (ссылка на литерал `<quotations>` — `0x1800fa16f`), отправка
`FUN_1800dba90`.
**В колбэк попадают только изменившиеся поля** — S: в r9 обновления HYDR MCT несут 1–2 поля,
а первые события после подписки — полный набор (31 значение маски).

Рендер колбэка: `<quotations><quotation secid="N"><board>B</board><seccode>CODE</seccode>` +
поля в фиксированном порядке (см. таблицу) + `</quotation></quotations>`.

## Карта бит маски → поля (S для значений, D для номеров)

Проверено скриптом на всех 16 событиях: 99 полей, 0 расхождений с XML DLL.

| Бит | Поле XML | Тип/масштаб |
|---|---|---|
| 0 | `biddeptht` | int |
| 1 | `numbids` | int |
| 2 | `offerdeptht` | int |
| 3 | `numoffers` | int |
| 4 | `biddepth` | int |
| 5 | `offerdepth` | int |
| 6 | `bid` | цена: `v / 10^decimals` (хвостовые нули срезаны, минимум 1 знак) |
| 7 | `offer` | цена |
| 8 | `numtrades` | int |
| 9 | `voltoday` | int |
| 10 | `openpositions` | int (futures) |
| 12 | `quantity` | int |
| 13 | `time` | если v < 86400 — секунды торгового дня → `HH:MM:SS`; иначе Unix-время → `dd.mm.yyyy HH:MM:SS` (S; точное правило — I) |
| 14 | `last` | цена |
| 15 | `change` | цена (знаковая) |
| 16 | `priceminusprevwaprice` | цена (знаковая) |
| 18 | `valtoday` | `v / 1000` (3 знака) |
| 20 | `waprice` | цена |
| 26 | `highbid` | цена |
| 27 | `lowoffer` | цена |
| 28 | `high` | цена |
| 29 | `low` | цена |
| 30 | `open` | цена |
| 31 | `closeprice` | цена |
| 33 | `status` | байт → символ (A — торгуется) |
| 34 | `tradingstatus` | байт → символ (N/T/0) |
| 42 | `lcurrentprice` | цена (бит 41 в тех же масках не выводится) |
| 54 | `buydeposit` | `v / 100` (2 знака) |
| 55 | `selldeposit` | `v / 100` (2 знака) |

`decimals` берётся из режима бумаги (R_SEC_NEW pit для пары secid+board): у SiZ6/RIZ6 — 0,
GAZP/TQBR — 2, HYDR/TQBR — 4, HYDR/MCT — 2 (**S**). Цена форматируется как `minstep` DLL:
деление на 10^decimals без хвостовых нулей, минимум одна цифра после точки; при decimals=0 —
целое (S).

**Не выведены в `<quotations>` (встречались, помечены I):** биты 17, 39, 40, 41, 56, 59 — DLL их
читает и хранит, но в колбэк не печатает (вероятно, `accruedintvalue`, `yield`/`yieldatwaprice`,
`marketpricetoday`, `theoreticalprice`, `bgo_buy`, `point_cost`; сверять с PDF 4.7 — I). Биты,
которые на демо не встречались, неизвестны.

## RE-3: разные decimals у одной бумаги — **S**

Пара найдена на демо и снята в одном прогоне: **HYDR** в двух режимах —
`TQBR` (board 37, market 1, `decimals=4`, secid 2) и `MCT` (board 45, market 14,
`decimals=2`, secid 8326). В r9 обе дали колбэки: TQBR `last=0.4968`, `bid=0.496`;
MCT `last=35.9`, `bid=35.62`, `change=0.66`. Сырые значения: TQBR bid 4960, MCT bid 3562 —
один и тот же `bits=6`, разный масштаб → `TestQuotationUsesBoardDecimals` можно строить на этой
паре (S). Время у MCT пришло как Unix-время (`04.11.2026 17:04:32`), у TQBR — как секунды дня
(`18:44:11`) — S для формата, I для причины (тип режима/серверное поле).

## Как использовать в тесте

```go
payload := readHex(t, "quotation-live.hex")          // 16 записей 0x06
recs := records.Records0x06(payload)                 // (W secid, W board, G mask, L×popcount(mask))
// декодировать поля по карте выше (значения — с учётом decimals режима), сравнить с
// quotations-dll.xml: по одному <quotation> на запись, порядок secid совпадает
```

Проверки: 16 записей ↔ 16 колбэков; значения 99 полей совпали; обновления несут только
изменившиеся биты. Тест должен также проверить маски RQ_SUBSCRIBE `0x01/0x05/0x00` и что
несколько `<quotations>` в одной команде обрабатываются как первая (S-поведение DLL).

## Обезличивание

Записи `0x06` — рыночные данные (secid, board, маска, цены/объёмы); логинов, клиентов, юнионов,
UID и номеров счетов в них нет. `secid`/`seccode` — публичные идентификаторы инструментов
(ММВБ). Сырые cap/логи DLL — только в `.re-run/` (gitignored).
