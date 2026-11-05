# marketord fixture: происхождение и воспроизведение (RE-3)

Пара `marketord-inject.hex` + `marketord-dll.xml` снята оракулом (replay-server + DLL
6.47.2.26.4 под Wine, только loopback) 2026-11-05.

## Вход (`marketord-inject.hex`, 8 байт)

Две записи RQ_MAIN `0x0e` R_SEC_MKT_PERMIT (без размера: `B tag, W secid, B val`):

| Байты | Смысл |
|---|---|
| `0e 001a 01` | secid 26 (SBER), допуск рыночных заявок = да |
| `0e 001a 00` | secid 26 (SBER), допуск рыночных заявок = нет |

## Выход (`marketord-dll.xml`, 2 колбэка)

```xml
<marketord secid="26" permit="yes" seccode="SBER"/>
<marketord secid="26" permit="no" seccode="SBER"/>
```

Событие приходит на каждую запись 0x0e (без дедупликации — O). Строка 0x0e также правит бит
0x04 поля флагов бумаги в сторе (D: `FUN_180264150`, case 4; см. `dictionaries.md`), поэтому
последующий `get_cln_sec_permissions` для SBER отдаёт `<marketable>false</marketable>` (O,
прогон `.re-run/t49/margin-final`, инъекция с двумя 0x0e перед командой).

## Обезличивание

Поля не содержат логина/клиента/юниона/UID; `secid`/`seccode` — публичные идентификаторы
инструмента. Обезличивать нечего.

## Воспроизведение

```bash
set -a; . ~/re/demo.env; set +a
.re-run/replay-server-t49 -dump .re-run/r14/decode-full.txt -listen 127.0.0.1:39800 \
  -after 148 -inject native/records/testdata/marketord-inject.hex &
RE_WORKDIR=/tmp/marketord-oracle .re-run/run-dll-prebuilt.sh 4s "<connect host=127.0.0.1 port=39800>" sleep:4s \
  '<command id="gethistorydata"><security><board>TQBR</board><seccode>SBER</seccode></security><period>1</period><count>1</count><reset>true</reset></command>' \
  sleep:2s sleep:3s '<command id="disconnect"/>'
```

Полный вывод — `.re-run/t49/marketord/last-run.out` (не в git).
