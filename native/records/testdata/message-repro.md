# message fixture: происхождение и воспроизведение (RE-3)

Пара `message-inject.hex` + `message-dll.xml` снята оракулом (replay-server + DLL 6.47.2.26.4
под Wine 11.21, только loopback, без живых операций) 2026-11-05.

## Вход (`message-inject.hex`, 72 байта)

Две записи RQ_MAIN `0x01` R_MSG (без размера: `B flags, Z20 from, L date, Z1024 text`,
C-строки переменной длины), подмешанные после запроса RQ_HISTORY_EXT (148):

| # | Байты (смысл) |
|---|---|
| 1 | `flags=0`, `from="TESTUSER"`, `date=packed(1762300000)`, `text="Test message text"` |
| 2 | `flags=1`, `from="TESTURGENT"`, `date=packed(1762300100)`, `text="Urgent message text"` |

Формат — `docs/protocol/messages.md`; упакованные целые — `tools/re/README.md` (RE-1).
`Z20`/`Z1024` — C-строки переменной длины (без добивки нулями).

## Выход (`message-dll.xml`)

```xml
<messages><message><date>23:46:40</date><urgent>N</urgent><from>TESTUSER</from><text><![CDATA[Test message text]]></text></message><message><date>23:48:20</date><urgent>Y</urgent><from>TESTURGENT</from><text><![CDATA[Urgent message text]]></text></message></messages>
```

`<date>` — только `HH:MM:SS` (локальное время машины; в записи — Unix-секунды настенного
времени сервера); `<urgent>` = Y для flags&1 или flags&8 (O).

## Обезличивание

Отправители заменены на `TESTUSER`/`TESTURGENT` (9 символов), даты и тексты синтетические;
логинов/клиентов/счетов в записях нет. Секретов в фикстуре нет.

## Воспроизведение

Dump сессии в git не входит (`.re-run/` gitignored; записан с демо-счёта, расшифровка паролем
из `~/re/demo.env`, значения не печатать).

```bash
set -a; . ~/re/demo.env; set +a
.re-run/replay-server-t49 -dump .re-run/r14/decode-full.txt -listen 127.0.0.1:39800 \
  -after 148 -inject native/records/testdata/message-inject.hex &
.re-run/tcproxy-t49 -listen 127.0.0.1:40200 -upstream 127.0.0.1:39800 -out /tmp/cap.log &
RE_WORKDIR=/tmp/message-oracle .re-run/run-dll-prebuilt.sh 4s "<connect host=127.0.0.1 port=40200>" sleep:4s \
  '<command id="gethistorydata"><security><board>TQBR</board><seccode>SBER</seccode></security><period>1</period><count>1</count><reset>true</reset></command>' \
  sleep:3s '<command id="disconnect"/>'
```

Полный вывод — `.re-run/t49/msg1/last-run.out` (не в git).
