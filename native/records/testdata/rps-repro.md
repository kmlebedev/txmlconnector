# rps fixtures: payload'ы РПС/РЕПО через RQ_TRANSACT (RE-3, оракул)

Снято DLL 6.47.2.26.4 под Wine 11.21 против `tools/replay-server` (только loopback; записанная
сессия + синтетический клиент TESTCLIEN и reply `type=14 = 01` через модификацию дампа).
**Реальных РПС/РЕПО-заявок не отправлялось** (сервер — проигрыватель, торговых операций нет).

## Файлы

| Файл | Команда | Что снято |
|---|---|---|
| `rps-transact-newrpsorder.hex` | `<command id="newrpsorder"><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>1</repoterm><reporate>10</reporate></command>` | RQ_TRANSACT (14) len=30: `B 04` (TRN_NEWRPSREPO), `W secid=26`, `W board=37`, `G client=4096`, `B 'B'` (buysell), далее служебные поля/цена/лот (частичный разбор — I) |
| `rps-transact-newrepoorder.hex` | `<command id="newrepoorder">…` (те же элементы + `<value>100</value>`) | RQ_TRANSACT len=39: `B 0e` (TRN_NEWREPOORDER), `W secid=26`, `W board=37`, `G client=4096`, `B 'B'`, далее `10 00 00 81 3e 02 71 02 10 41 06 41 06 41 06 4e 00 00` (поля repoterm/reporate/value/дисконты — частично I) |
| `rps-transact-cancel.hex` | `<command id="cancelnegdeal"><transactionid>1</transactionid></command>` и `<command id="cancelreport4"><reportid>1</reportid></command>` | RQ_TRANSACT len=2: `B 08`, `L 1` (packed) — одинаковые байты для обеих команд (какой TRN-код различает — I) |

## Поведение DLL (O)

- Пустые команды: newrpsorder/newrepoorder/newmrepoorder → `Не указан элемент "secid"`;
  cancelnegdeal → `You have to specify transactionid.`; newreport4 →
  `No "ustrade_leg" element found`; cancelreport4 → `You have to specify reportid.`.
- С элементами `secid/client/buysell/quantity`: newrpsorder → `Клиент не допущен к торгам.`;
  newrepoorder → `Specified security can't be traded.`; cancelnegdeal при reply status=1 →
  `{8} Неверное значение параметра 'TransactionID'.`; cancelreport4 → `Unknown error`.
- Это ответы DLL на тестовой сессии (клиент не РПС-ный), реальная серверная логика — S-серия.

## Воспроизведение

```bash
set -a; . ~/re/demo.env; set +a
python3 .re-run/t49/mk-rps-dump.py late   # dump с клиентом TESTCLIEN и reply 14=01
.re-run/t49/oracle-margin.sh rps -dump .re-run/t49/dump-rps2.txt -- \
  '<command id="newrpsorder"><secid>26</secid><client>TESTCLIEN</client><buysell>B</buysell><quantity>1</quantity><price>100</price><repoterm>1</repoterm><reporate>10</reporate></command>'
```

Захват: C>S `type=14 <hex>`; S>C `type=14 01`. Полные выводы — `.re-run/t49/rps{1,2,3,4}/`
(вне git).

## Обезличивание

client id в запросах = 4096 (dummy TESTCLIEN), в payload нет логина/пароля/union; secid/board —
публичные идентификаторы инструмента. Секретов нет.
