# РПС и РЕПО: newrpsorder, newrepoorder, newmrepoorder, cancelnegdeal, newreport4, cancelreport4 — RQ_TRANSACT (Task 28)

Дата: 2026-11-05. Источник: RE-1 (декомпилятор DLL 6.47.2.26.4) + RE-3 (оракул: replay-server +
DLL под Wine, только loopback, синтетический клиент). Статусы: **S** — демо-сервер, **O** —
оракул DLL, **D** — декомпилятор, **I** — вывод.

> **Ограничение.** Реальных РПС/РЕПО-заявок не отправлялось: сервер — проигрыватель записанной
> сессии на loopback, `type=14` отвечал синтетическим статусом. Живой демо-сервер не
> использовался (утренняя S-серия).

## Команды (D)

| Команда | Обработчик (D) | Ядро (D) | Результат |
|---|---|---|---|
| `newrpsorder` | `FUN_180198560` | `FUN_1800e4af0` → `FUN_1800e4fd0` | `<result transactionid="N"/>` |
| `newrepoorder` | `FUN_1801984e0` | `FUN_1800e4c90` → `FUN_1800e6c70` | `<result transactionid="N"/>` |
| `newmrepoorder` | `FUN_1801984a0` | `FUN_1800e4e30` → `FUN_1800e8b40` | `<result transactionid="N"/>` |
| `cancelnegdeal` | `FUN_180197ee0` | `FUN_18010a7d0` (→ `FUN_18037a7d0(conn,6,txid,…)`) | `<result transactionid="N"/>` |
| `newreport4` | `FUN_180198520` | `FUN_1800eb530` → `FUN_18010aa00` | `<result transactionid="N"/>` |
| `cancelreport4` | `FUN_180197f60` | `FUN_1800eb900` | `<result transactionid="N"/>` |

## Входные элементы (D + O)

Общие для РПС/РЕПО-заказов: `<secid>`, `<client>`/`<union>`, `<buysell>` (`B`/`S`),
`<quantity>`, и параметры сделки: `<price>`, `<repoterm>`, `<reporate>`, `<value>`,
`<startdiscount>`, `<lowerdiscount>`, `<upperdiscount>`, `<blocksecurities>` (`Y`/`N`),
`<refundrate>`, `<matchref>`, `<cpfirmid>`, `<settlecode>`; для `newreport4` —
`<ustrade_leg>`; для `cancelnegdeal` — `<transactionid>`; для `cancelreport4` —
`<reportid>` (D-литералы, адреса — ниже).

### Ошибки (O, дословно)

| Команда | Пустой запрос → ответ DLL |
|---|---|
| newrpsorder/newrepoorder/newmrepoorder | `Не указан элемент "secid"` |
| cancelnegdeal | `You have to specify transactionid.` |
| newreport4 | `No "ustrade_leg" element found` |
| cancelreport4 | `You have to specify reportid.` |

Дополнительно (D/O): `Client not specified.`, `Wrong client specifited.`, `Client not found.`,
`Cannot find buysell tag.`, `Buysell can be only 'B' or 'S'.`, `Cannot find quantity.`,
`Quantity have to be a number.`, `Specified security can't be traded.` (O),
`Клиент не допущен к торгам.` (O), `Value not found.`, `Value have to be a number.`,
`Repoterm have to be a number.`, `RepoRate not found.`, `RepoRate have to be a number.`,
`Unknown error` (O, cancelreport4), `Transaction identifier have to be a number.`,
`Reportid identifier have to be a number.`.

## RQ_TRANSACT (14): payload'ы (O + частичный D-разбор)

Все шесть команд шлют обычный RQ_TRANSACT (type=14) — общий построитель/разборщик транзакций
(task 15). Снятые payload'ы (фикстуры `native/records/testdata/rps-*.hex`):

### newrpsorder — 30 байт

```
04            B  TRN_NEWRPSREPO (код транзакции)
001a          W  secid 26 (SBER)
0025          W  board 37 (TQBR)
02 0001       G  client id = 4096 (packed, dummy TESTCLIEN)
42            B  'B' (buysell)
00            B  служебный флаг
0000000000 0000 00 00 00 00  служебные нули (repoterm/reporate/value по умолчанию)
027102        L  price = 10000 (100.00)
10            L  quantity = 1
0000000000 00 00 00 00 00  хвост нулей
```

O: `<command id="newrpsorder"><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>1</repoterm><reporate>10</reporate></command>`
→ RQ_TRANSACT len=30, ответ DLL `Клиент не допущен к торгам.` (клиент не РПС-ный; реальная
серверная логика — S). Точные смещения полей repoterm/reporate/value — **I** (нули в снятом
payload; уточнить в S/D).

### newrepoorder — 39 байт

```
0e            B  TRN_NEWREPOORDER
001a 0025     W,W secid 26 / board 37
02 0001       G  client 4096
42            B  'B'
00            B
000000 0000 0000 0000 0000 0000
10            L  repoterm? = 1
0000813e      L  ? (≈ value/vale 100.00?)
027102        L  price 10000
10            L  quantity 1
4106 4106 4106  L,L,L  discount-поля (значения 1.00? — packed 0x106)
4e 0000       B,B  blocksecurities/final
```

O: результат — `Specified security can't be traded.`; точный смысл полей `10/0000813e/4106...`
— **I** (нужен D-разбор ядра `FUN_1800e6c70`).

### cancelnegdeal / cancelreport4 — 2 байта

```
08            B  код отмены (FUN_18037a7d0(conn,6,txid) — соответствие 6→0x08 — I)
10            L  packed L: transactionid/reportid = 1
```

O: обе команды дали одинаковые байты `08 10`; ответ на reply status=1 —
`{8} Неверное значение параметра 'TransactionID'` (cancelnegdeal) / `Unknown error`
(cancelreport4). Какой TRN-код различает отмену negdeal и отчёт — **I**.

## RQ_RPSREPO_SUBSCR (72) — D

Построитель `FUN_1803b7b00`: `FUN_1801c2030(conn+0xbd0,1)`; `B param_3` (`FUN_1801be4f0`);
`FUN_180355d50(conn, conn+0xbd0, 0x48, "RQ_RPSREPO_SUBSCR")`. Payload — 1 байт (флаг подписки);
живой подписки на демо не наблюдалось (**S**-негатив в утренней серии).

## RQ_REPORT_INFO (21) / RQ_GET_REPORT (22) — D

- 21 (`FUN_1803b2ff0`): `FUN_1801c2030(stream,1)`; `L reportid` (`FUN_1801bebb0`);
  `FUN_180355d50(...,0x15,"RQ_REPORT_INFO")`; ответ → `<report …>` (литералы ниже).
- 22 (`FUN_1803b3580`): RQ_GET_REPORT; детальный разбор ответа — **I**.

## События/XML-формы (D-литералы; O-форм нет — I)

- `<negdeal transactionid="…">` `0x180830b08`, `</negdeal>` `0x1808311a0`; внутри
  `<secid>` `0x180830af0`/`</secid>` `0x180831358`, `<union>`/`</union>`,
  `<client>`/`</client>`, `<board>`/`</board>`, `<seccode>`/`</seccode>`,
  `<buysell>`/`</buysell>`, `<price>`/`</price>`, `<volume>`/`</volume>`,
  `<repoterm>`/`</repoterm>`, `<reporate>`/`</reporate>`, `<repovalue>`/`</repovalue>`,
  `<repo2value>`/`</repo2value>`; ошибки-строки `Callback negdeal inner loop`,
  `Attributes 'union' and 'client' are empty`, `Attribute 'union' has no value`,
  `Callback report inner loop`, `Can't get report from cache`,
  `Failed to format report time: gmtime error`, `Delayed USTradeReported`, маркеры
  `'RPS'`, `'REPO'`, `'EXTREPO'`, `'EXTCREPO'`.
- `<report transactionid="…">` `0x1808319d8`, `</report>` `0x180831d28`; внутри
  `<reportno>`/`</reportno>`, `<reportkind>`/`</reportkind>`, `<reporttype>`/`</reporttype>`,
  `<repotradeno>`/`</repotradeno>`, `<repovalue>`/`</repovalue>`,
  `<repo2value>`/`</repo2value>`.
- Точные формы (порядок, атрибуты, какие события PDF 5.7/5.8) — **I**: XML не снят оракулом;
  обработка идёт через большой генератор `FUN_1800f7b40` (декомпиляция превышает лимит).
  Для T28 реализация может опираться на D-литералы, побайтовая сверка — после S.

## S-негатив / не проверено

- **S-фактов нет**: живой демо-сервер не использовался (утренняя S-серия); РПС/РЕПО на демо, как
  правило, недоступны — проверить и зафиксировать честно.
- Полные layout'ы newrepoorder/newmrepoorder/newreport4 и семантика полей — **I** (нужен
  D-разбор ядер `FUN_1800e6c70/1800e8b40` и элемента `ustrade_leg`).
- XML-формы `<negdeal>`/`<report>` и RQ_MAIN-записи для сделок/отчётов — **I** (только литералы).
- RQ_GET_REPORT (22) — детали не разобраны в этой карточке.

## Фикстуры (`native/records/testdata`)

| Файл | Что | Статус |
|---|---|---|
| `rps-transact-newrpsorder.hex` | RQ_TRANSACT payload newrpsorder (30 Б) | O |
| `rps-transact-newrepoorder.hex` | RQ_TRANSACT payload newrepoorder (39 Б) | O |
| `rps-transact-cancel.hex` | RQ_TRANSACT payload cancelnegdeal/cancelreport4 (2 Б, `08 10`) | O |
| `rps-repro.md` | происхождение, обезличивание, воспроизведение | — |

## Источники

D: `tools/re/ghidra/XrefDecomp.java` по `txmlconnector64-6.47.2.26.4.dll`; функции — выше.
O: `tools/replay-server` + `tools/tcproxy` + DLL под Wine 11.21, dump `.re-run/r14/decode-full.txt`
(вне git) + модификация дампа (`.re-run/t49/mk-rps-dump.py`); прогоны `.re-run/t49/rps{1..4}`.
