# option fixtures: происхождение и воспроизведение (RE-3)

Снято DLL 6.47.2.26.4 под Wine 11.21 против `tools/replay-server` (только loopback, записанная
сессия) — команда `get_option_families`, захват `tcproxy`. 2026-11-05.

## Вход/выход

| Файл | Что |
|---|---|
| `option-families-request.hex` | payload C>S `type=149` (RQ_OPTION_FAMILIES): `00 1a` = `W secid=26` (SBER) — **O**, 2 байта |
| `option-families-reply-ok.hex` | reply `type=149` = `00` (пустая C-строка) → `<result success="true"/>` — **O** |
| `option-families-reply-error.hex` | reply `type=149` = `4e6f2066616d696c69657300` ("No families") → `<result success="false"><message>No families</message></result>` — **O** |

XML-колбэки `<option_families>`/`<family_strikes>`/`<options_failed>` оракулом снять не
удалось (см. `docs/protocol/options.md`, раздел I): инъекция RQ_MAIN-записей 0x6f/0x71/0x78 до и
после команды не даёт колбэков; `get_family_strikes`/149-гейт версии — сервер в записанной сессии
сообщает версию 68 (<0x45), DLL отвечает `Unsupported request.`.

## Воспроизведение

```bash
set -a; . ~/re/demo.env; set +a
# ok-вариант
.re-run/t49/oracle-margin.sh optok -reply-to 149 -reply-type 149 \
  -inject native/records/testdata/option-families-reply-ok.hex -- \
  '<command id="get_option_families"><security><board>TQBR</board><seccode>SBER</seccode></security></command>'
# error-вариант — с option-families-reply-error.hex
```

Захват: C>S `type=149 len=2 001a`; S>C `type=149 00` (или текст+NUL). Полные выводы —
`.re-run/t49/opt{,A,B,C,D}/` (вне git).

## Обезличивание

Опционные коды синтетические (`OPT01`, `SBEROPT1/2`), клиентов/логинов в payload нет;
секретов в фикстурах нет.
