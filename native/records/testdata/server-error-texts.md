# Server error texts: O-снимок для TestServerErrorTexts (T29)

Дата: 2026-11-05. Источник: RE-3 оракул на записанной сессии (DLL 6.47.2.26.4 под Wine +
replay-server, loopback), см. `docs/protocol/errors.md`. Реальных торговых команд не было.

## RQ_TRANSACT (14): fake reply = один байт кода

| Reply (hex) | Код | Ответ DLL (`<message>`) |
|---|---|---|
| `01` | 1 | Клиент не допущен к торгам. |
| `02` | 2 | Трейдер не допущен к торгам. |
| `03` | 3 | Клиент не обслуживается данным Трейдером. |
| `04` | 4 | Сделки с собой запрещены. |
| `05` | 5 | Попытка снять несуществующую заявку. |
| `06` | 6 | Транзакции не разрешены. |
| пусто (len=0) | — | `Unknown error` + лог `RQ_TRANSACT … return incorrect buffer` |
| `22` (34) | 34 | `Unknown error` (34 — не код сервера; D-таблица DLL, см. errors.md) |

Полный D-список текстов: Win32 RT_STRING, язык 1049, ID = 615 + код (коды 1..66; строки с
параметрами — `%s`/`%I64d`). O сняты коды 1..6; остальные — D/I.

## Воспроизведение

```bash
set -a; . ~/re/demo.env; set +a
python3 .re-run/t49/mk-err-dump.py codes 1 2 3 4 5 6   # дамп с fake reply type=14
.re-run/t49/oracle-margin.sh errT2 -dump .re-run/t49/dump-err-codes.txt -- \
  '<command id="neworder"><secid>26</secid><client>TESTCLIEN</client><price>1</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>'
```

Сырьё — `.re-run/t49/errT2`, `errT3` (вне git). Обезличивание: client = TESTCLIEN (dummy id 4096).
