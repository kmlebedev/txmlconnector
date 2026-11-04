# news fixtures: происхождение и воспроизведение (RE-3)

Пара `news-inject.hex` + `news-dll.xml` снята оракулом (replay-server + DLL 6.47.2.26.4 под
Wine 11.21, только loopback, без живых операций) 2026-11-05.

## Вход (`news-inject.hex`, 116 байт)

Записи RQ_MAIN, подмешанные в поток ответа RQ_MAIN после запроса RQ_HISTORY_EXT (148):

| Запись | Что задаёт |
|---|---|
| `0x35` R_NEWS (без размера) | заголовок: `L id=1001`, `L timestamp=1762300000`, `Z16 source="TESTNEWSSOURCE"`, `Z128 title="Test news title"`, `B flag=0` |
| `0x36` R_NEWS_UPLOAD (без размера) | `B flags=3`: заголовок `L id=2002`, `L timestamp=1762300100`, `Z16 source`, `Z128 title="Upload news title"`, `B`; тело `L len`, package = `int32 19 + zlib("Test news body text")` |

Форматы записей — `docs/protocol/news.md`; упакованные целые — `tools/re/README.md` (RE-1).
`Z16`/`Z128` — C-строки переменной длины (без добивки нулями; иначе DLL «съезжает» на
следующем поле — проверено).

## Выход (`news-dll.xml`, 2 колбэка)

```xml
<news_header><id>1001</id><timestamp>04.11.2025 23:46:40</timestamp><source><![CDATA[TESTNEWSSOURCE]]></source><title><![CDATA[Test news title]]></title></news_header>
<news_header><id>2002</id><timestamp>04.11.2025 23:48:20</timestamp><source><![CDATA[TESTNEWSSOURCE]]></source><title><![CDATA[Upload news title]]></title></news_header>
```

Колбэк `<news_body>` в этом и последующих прогонах не появился (тело в очереди есть, но путь
выдачи тела оракулом не подтверждён — см. `docs/protocol/news.md`, раздел I).

## Обезличивание

Новости синтетические: id 1001/2002, source `TESTNEWSSOURCE`, заголовки/текст — тестовые
строки; логинов, клиентов, счетов и юнионов в записях нет. Секретов в фикстуре нет.

## Воспроизведение

Dump сессии в git не входит (`.re-run/` gitignored; записан с демо-счёта, расшифровка паролем
из `~/re/demo.env`, значения не печатать).

```bash
set -a; . ~/re/demo.env; set +a
.re-run/replay-server-t49 -dump .re-run/r14/decode-full.txt -listen 127.0.0.1:39800 \
  -after 148 -inject native/records/testdata/news-inject.hex &
.re-run/tcproxy-t49 -listen 127.0.0.1:40200 -upstream 127.0.0.1:39800 -out /tmp/cap.log &
RE_WORKDIR=/tmp/news-oracle .re-run/run-dll-prebuilt.sh 4s "<connect host=127.0.0.1 port=40200>" sleep:4s \
  '<command id="gethistorydata"><security><board>TQBR</board><seccode>SBER</seccode></security><period>1</period><count>1</count><reset>true</reset></command>' \
  sleep:2s '<command id="get_old_news" count="2"/>' '<command id="get_news_body" news_id="2002"/>' \
  sleep:2s '<command id="disconnect"/>'
```

Полный вывод — `.re-run/t49/news-final/last-run.out` (не в git); пейлоады RQ_GETNEWS — в
`.re-run/t49/news-final/decode.txt`.
