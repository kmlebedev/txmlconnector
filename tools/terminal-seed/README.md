# terminal-seed — снапшот рыночных данных для офлайн-демо

Утилита снимает **read-only** снапшот демо-сервера TRANSAQ (Finam) через нативный клиент
`native.Client` (без DLL/Wine) и пишет `terminal/seeddata/data/seed.json.gz` — встроенные данные
офлайн-режима AI-терминала. Схема — `docs/ai-terminal/ARCHITECTURE.md` §5.

Снимаются только рыночные данные: свечи периодов 60/300/900/3600/86400 с, котировки, стаканы,
лента сделок и справочные поля инструментов (лот, шаг цены, точность). Счета, клиенты, логины и
юнионы не записываются.

```bash
# секреты берутся из окружения и никогда не печатаются
set -a; . ~/re/demo.env; set +a            # TC_HOST/TC_PORT/TC_LOGIN/TC_PASSWORD
export GOCACHE="$PWD/.cache/go-build"
go run ./tools/terminal-seed -out terminal/seeddata/data/seed.json.gz
```

Флаги: `-addr host:port` (переопределяет `TRANSAQ_ADDR`), `-universe SBER,GAZP,...` (по умолчанию
14 бумаг TQBR), `-out` (путь к `.json.gz`), `-wait 8s` (сбор котировок/стакана/ленты).

Требования: демо-сервер доступен (09:50–23:50 МСК, будни), логин/пароль — только через env.
Данные демо-сервера могут иметь «демо-датировку»; seed фиксирует реальные UTC-моменты, которые
отдаёт native-клиент (исправление единиц времени свечей — `docs/protocol/history.md`).
