# margin fixtures: происхождение и воспроизведение (RE-3)

Пара `margin-inject.hex` + `margin-dll.xml` снята оракулом (replay-server + DLL 6.47.2.26.4 под
Wine 11.21, только loopback, без живых торговых команд) 2026-11-05.

## Вход (`margin-inject.hex`, 86 байт)

Четыре записи RQ_MAIN, подмешанные в поток ответа RQ_MAIN после запроса RQ_HISTORY_EXT (148):

| Запись | Байты | Что задаёт |
|---|---|---|
| `0x02*` R_CLN_NEW | `020021…` (36 Б) | клиент id=4096, код `TESTCLIEN`, логин `TESTLOGIN`, тип 2 (leverage), market 1, currency 1 (RUB) |
| `0x3b*` R_CLIENT_ASSET | `3b0015…` (24 Б) | денежная позиция клиента 4096: `sym=0xffff`, reg=1, mask=0x03, open_balance=balance=30 000 000 (300 000.00 RUB), mask2=0x04, currency RUR |
| `0x3b*` R_CLIENT_ASSET | `3b0011…` (20 Б) | бумажная позиция SBER (`sym=26`): reg=1, mask=0x03, open_balance=balance=1000, mask2=0x04, currency RUR |
| `0x0a` R_SEC_RATE | `0a 001a a21d07` (5 Б) | цена SBER для маржинального расчёта: `W secid=26`, packed L = 29146 (291.46 ₽ в копейках) |

Формат записей — `docs/protocol/connect.md` (R_CLN_NEW), `docs/protocol/positions.md`
(R_CLIENT_ASSET), `docs/protocol/margin.md` (R_SEC_RATE → `symbol+0xb8`); упакованные целые —
`tools/re/README.md` (RE-1). Запись `0x0a` задаёт цену явно, поэтому ожидаемые значения
`margin-dll.xml` (13210/15210) не зависят от цены, загруженной из записанной сессии, и
воспроизводятся формулой из `margin.md`:
`maxbuy = trunc(300000·100/(0.1428·29146) + 1000·(1−0.1428)/0.1428) = 13210`,
`maxsell = … + 1000·(1+0.1428)/0.1428 = 15210`.

## Выход (`margin-dll.xml`, 3 колбэка)

DLL на команды (после arm-запроса `gethistorydata` и паузы, чтобы инъекция дошла):

1. `<command id="get_max_buy_sell" client="TESTCLIEN"><security><seccode>SBER</seccode><market>1</market></security></command>`
   → `<max_buy_sell client="TESTCLIEN"><security secid="26"><market>1</market><seccode>SBER</seccode><maxbuy>13210</maxbuy><maxsell>15210</maxsell></security></max_buy_sell>`
2. `<command id="get_max_buy_sell_tplus" client="TESTCLIEN">…` (то же тело) →
   `<max_buy_sell_tplus client="TESTCLIEN">…</max_buy_sell_tplus>` с теми же значениями.
3. `<command id="get_cln_sec_permissions"><security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security><client>TESTCLIEN</client></command>`
   → `<cln_sec_permissions client="TESTCLIEN" secid="26">…<tradeable>true</tradeable><marketable>true</marketable><longable>true</longable><shortable>true</shortable><regratelong>14.28</regratelong><regrateshort>14.28</regrateshort></cln_sec_permissions>`.

Побочные колбэки инъекции (`<client id="TESTCLIEN">`, `<positions><sec_position…>`,
`<money_position…>`) в файл не включены — их форма закреплена фикстурами T7/T20
(`position-sec-inject.hex` + `position-sec-dll.xml`).

## Обезличивание

Код клиента заменён на `TESTCLIEN` (9 символов, как `virt/9993`), логин — на `TESTLOGIN`
(9 символов, C-строка той же длины), id клиента 4096 — синтетический. Денежные значения
(300 000.00 RUB) и позиция (1000 SBER) — синтетические, как в фикстурах T7/T20. `secid`/`seccode`
(SBER 26 / TQBR) — публичные идентификаторы инструмента. Секретов в фикстуре нет.

## Воспроизведение

Dump сессии в git не входит (содержит данные счёта, `.re-run/` gitignored); он записан с
демо-счёта и расшифрован паролем из `~/re/demo.env` (`TC_PASSWORD`, значения не печатать).

```bash
set -a; . ~/re/demo.env; set +a
go build -o .re-run/replay-server-t49 ./tools/replay-server
go build -o .re-run/tcproxy-t49 ./tools/tcproxy
# 1) проигрыватель записанной сессии, инъекция после RQ_HISTORY_EXT (148)
.re-run/replay-server-t49 -dump .re-run/r14/decode-full.txt -listen 127.0.0.1:39800 \
  -after 148 -inject native/records/testdata/margin-inject.hex &
# 2) DLL под Wine подключается к loopback; tcproxy фиксирует кадры C>S
.re-run/tcproxy-t49 -listen 127.0.0.1:40200 -upstream 127.0.0.1:39800 -out /tmp/cap.log &
# 3) harness: connect -> gethistorydata (arm) -> пауза -> три команды -> disconnect
RE_WORKDIR=/tmp/margin-oracle .re-run/run-dll-prebuilt.sh 4s "<команда connect host=127.0.0.1 port=40200>" sleep:4s \
  '<command id="gethistorydata"><security><board>TQBR</board><seccode>SBER</seccode></security><period>1</period><count>1</count><reset>true</reset></command>' \
  sleep:2s '<command id="get_max_buy_sell" …/>' '<command id="get_max_buy_sell_tplus" …/>' '<command id="get_cln_sec_permissions">…</command>' \
  sleep:3s '<command id="disconnect"/>'
```

Полные команды прогона и XML ответов — в `.re-run/t49/margin-fixture/` (не в git).
