# Инструменты обратной разработки протокола TRANSAQ

Этот каталог — всё, что использовалось для восстановления протокола `txmlconnector64-6.47.2.26.4.dll` при работе над `native/`. Он написан для ассистента или разработчика, который продолжит работу по плану [`docs/superpowers/plans/2026-11-04-native-transaq-client.md`](../../docs/superpowers/plans/2026-11-04-native-transaq-client.md). Что уже восстановлено — в [`docs/protocol/`](../../docs/protocol).

## Правила

- Цель — совместимость: Go-клиент повторяет поведение DLL. Эталон — DLL с SHA-256 `a197e2625e53c0df453687ae1b20ef2ae1a05e034cd20c424a7014b0c9726342` (лежит в корне репозитория).
- Учётные данные демо-счёта — только в окружении (`TC_LOGIN`, `TC_PASSWORD`). Их не пишут в файлы репозитория, сообщения коммитов, PR и ответы пользователю.
- Захваты живых сессий (`*.cap`, дампы `wire-decode`, вывод harness) содержат данные счёта. Их хранят вне git. Фикстуры в `testdata/` — только обезличенные или синтетические.
- Демо-сервер Finam: `tr1-demo5.finam.ru:3939`, 09:50–23:50 МСК, кроме выходных и праздников. Сделки и котировки идут в основную сессию биржи; вечером демо-сервер их может не присылать.
- Заявки, отмены и смену пароля живьём отправляют **только** с явного согласия пользователя, на демо-счёт, минимальным лотом.
- Утверждения в `docs/protocol/*.md` помечаются статусом: **S** — демо-сервер, **O** — оракул DLL, **D** — декомпилятор, **I** — вывод.

## Состав

| Путь | Что делает |
|---|---|
| `ghidra/XrefDecomp.java` | декомпилирует функции по адресам `a:<hex>` или по ссылкам на строку `s:<подстрока>`, с вызываемыми функциями на заданную глубину |
| `ghidra/CallArgs.java` | для всех вызовов функции восстанавливает константу в `R8D` и строку в `R9` (так построена карта «тип запроса → имя → построитель» по `FUN_180355d50`) |
| `ghidra/Callers.java` | кто вызывает функцию |
| `ghidra/StrRefs.java` | точное совпадение строки и инструкции вокруг ссылок на неё (поиск регистрации команд) |
| `ghidra/ScalarRefs.java` | инструкции с заданной константой (так найден обработчик сообщения потока `0x432`) |
| `py/txproto.py` | протокол на Python: ГОСТ (гамма/CFB), CRC-16, ключ пароля, кадры, упакованные целые, разбиение RQ_MAIN, чтение дампов. Таблицы берутся из `native/cryptoprofile/tables.go` |
| `py/selftest.py` | проверка `txproto.py` на векторах, снятых с DLL |
| `py/proxy.py` | логирующий TCP-прокси клиент ↔ сервер (формат захвата для `tools/wire-decode`) |
| `py/sink.py` | TCP-приёмник без сервера: записывает, что шлёт клиент, может ответить сырыми байтами |
| `py/stub_server.py` | сервер со сценарием: логин, выдача сессионного ключа, ответы по типам запросов, печать каждого кадра клиента |
| `py/replay_server.py` | проигрывает записанную сессию и подмешивает записи в RQ_MAIN после заданного запроса (оракул) |
| `../tcproxy` | Go-порт `py/proxy.py`: логирующий прокси с тем же форматом захвата (RE-2) |
| `../replay-server` | Go-порт `py/replay_server.py`: проигрыватель захвата с инъекцией записей RQ_MAIN (RE-3) |
| `py/make_alltrades.py` | генератор записей `R_ALLTRADE`; наборы 1–3 = фикстуры `native/xmlcompat/testdata/alltrades-inject*.hex`; образец для других записей |
| `py/analyze_startup.py` | разбор ответа RQ_STARTUP и побайтная сверка `<markets>`/`<boards>`/`<candlekinds>` с DLL |
| `py/analyze_main.py` | статистика тегов RQ_MAIN, разбор `R_SEC_NEW`, таблицы корреляции «поле записи ↔ элемент XML» |
| `py/dll_maps.py` | карты «команда → обработчик» и «тег RQ_MAIN → обработчик» из декомпиляции |
| `run_dll.sh` | запуск DLL под Wine через `tools/reference-harness` |
| `../reference-harness` | Go-harness: XML-команды в DLL (`-backend dll`) или в `native.Connector` (`-backend native`), шаги `sleep:<длительность>` |
| `../wire-decode` | расшифровка захвата (нужен `TC_PASSWORD`) |
| `../native-login`, `../native-trades` | живая проверка Go-клиента |
| `../../docs/research/inspect_binary.py` | паспорт PE: экспорты, секции, строки |

## Окружение

Проверено на macOS 26 (Apple Silicon). На Linux x86-64 Wine ставится из пакетов дистрибутива, Rosetta не нужна.

```bash
# Rosetta (macOS arm64), если ещё нет
softwareupdate --install-rosetta --agree-to-license

# Wine для macOS (x86_64, работает под Rosetta); в core Homebrew cask отключён
mkdir -p ~/re/wine && cd ~/re/wine
gh release download 11.21 -R Gcenx/macOS_Wine_builds -p 'wine-devel-11.21-osx64.tar.xz'
tar -xJf wine-devel-11.21-osx64.tar.xz
export WINE="$HOME/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine"
export WINEPREFIX="$HOME/re/wine-prefix"
"$WINE" wineboot -i

# Ghidra (Homebrew ставит openjdk@25; analyzeHeadless без TTY требует JAVA_HOME)
brew install ghidra
export JAVA_HOME=/opt/homebrew/opt/openjdk@25/libexec/openjdk.jdk/Contents/Home
export GHIDRA_HEADLESS=/opt/homebrew/opt/ghidra/libexec/support/analyzeHeadless

# для фазы с NATS/protobuf
brew install protobuf nats-server
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
```

Проверка Python-инструментов: `python3 tools/re/py/selftest.py` → `txproto selftest: OK`.

Проверка DLL под Wine:

```bash
export WINE="$HOME/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine"
export WINEPREFIX="$HOME/re/wine-prefix"
export TXC_DLL=$PWD/txmlconnector64-6.47.2.26.4.dll
tools/re/run_dll.sh 1s '<command id="get_connector_version"/>'
# CALLBACK: <connector_version>6.47.2.26.4</connector_version>
```

На текущей машине Wine 11.21 и prefix уже установлены по этим путям; `run_dll.sh` подставляет
`WINE`/`WINEPREFIX`/`TXC_DLL` по умолчанию, если переменные не заданы (проверено 2026-11-04).
Секреты и Wine-экспорты удобно держать в `~/re/demo.env` (chmod 600, шаблон —
`tools/re/demo.env.example`): `set -a; . ~/re/demo.env; set +a` перед живыми командами.
Заполненный `*.env` в git не попадает.

Проект Ghidra создаётся один раз, анализ занимает несколько минут. Каталог проекта держите вне репозитория:

```bash
mkdir -p ~/re/ghproj
"$GHIDRA_HEADLESS" ~/re/ghproj txc -import "$TXC_DLL" -analysisTimeoutPerFile 3000 -max-cpu 8
```

## Процедуры

На них ссылается план: RE-1…RE-4.

### RE-1. Декомпиляция

```bash
G() { "$GHIDRA_HEADLESS" ~/re/ghproj txc -process txmlconnector64-6.47.2.26.4.dll -noanalysis -readOnly \
        -scriptPath tools/re/ghidra -postScript "$@" > /dev/null 2>&1; }
G XrefDecomp.java /tmp/out.c 1 a:1803bf700            # функция и её вызовы на 1 уровень
G XrefDecomp.java /tmp/out.c 0 "s:incorrect server response"   # все функции со ссылкой на строку
G CallArgs.java  /tmp/rq.txt 180355d50                 # тип → имя → построитель запросов
G Callers.java   /tmp/callers.txt 180355ce0 18038d7d0  # кто вызывает
G StrRefs.java   /tmp/strs.txt server_status           # точная строка и код вокруг
G ScalarRefs.java /tmp/k.txt 432                       # инструкции с константой 0x432
python3 tools/re/py/dll_maps.py commands /tmp/cmdtable.c   # C функции FUN_1801774b0
python3 tools/re/py/dll_maps.py tags /tmp/dispatch.c       # C функции FUN_18038d7d0
```

`analyzeHeadless` возвращает 0 даже при ошибке скрипта: проверяйте, что выходной файл заканчивается на `/* done N functions */`. Если функция слишком большая («Response buffer size exceeded» — так с генератором `<alltrades>` `FUN_1800f7b40`), используйте оракул RE-3.

Как читать декомпиляцию. Поток — объект `param_2`; курсор чтения/записи — `+0xe0`, конец данных — `+0xd0`, граница записи — `+0xf8`. Примитивы:

| Чтение | Формат | Запись | Формат |
|---|---|---|---|
| `FUN_1801bd290`, `FUN_1801bd510` | `B` — байт | `FUN_1801be680` (`C`), `FUN_1801be4f0` | байт |
| `FUN_1801bd5d0`, `FUN_1801bd380` | `W` — u16 BE | `FUN_1801be710` | `W` |
| `FUN_1801bd7b0` | `L` — упакованное 32-битное | `FUN_1801be7d0` | `L` |
| `FUN_1801bd960` | `L`, расширенное до int64 | — | — |
| `FUN_1801bd9b0` | `G` — упакованное 64-битное (счётчик 7 = 8 байт) | `FUN_1801be970` | `G` |
| `FUN_1801bd170(r, n, dst)` | `Zn` — строка до n байт + NUL | `FUN_1801be3e0(w, n, s)` | `Zn` |
| `FUN_1801bd420(r, n, dst)` | `n` байт без длины | `FUN_1801be5d0` | `n` байт |
| `FUN_1801bdbc0` | `D` — double LE, 8 байт | | |
| `FUN_1801bdf00(r, 0, 2)` | размер записи `W`, ставит границу | | |
| `FUN_1801be120(r, 0)` | «остались байты до границы» — необязательные хвостовые поля | | |

Упакованное целое: первый байт — `(|v| & 0xf) << 4 | знак << 3 | k`, затем `k` байт `|v| >> 4` little-endian.

Запросы строит `FUN_180355d50(conn, stream, тип, "RQ_ИМЯ", …)`: payload, записанный перед вызовом, — тело запроса; чтение после вызова — разбор ответа. Карты команд, запросов и тегов уже выписаны в плане.

### RE-2. Живой захват DLL

```bash
set -a; . ~/re/demo.env; set +a     # TC_LOGIN, TC_PASSWORD (вне репозитория)
python3 tools/re/py/proxy.py 39100 tr1-demo5.finam.ru 3939 ~/re/cap/session.cap &
CONN="<command id=\"connect\"><login>$TC_LOGIN</login><password>$TC_PASSWORD</password><host>127.0.0.1</host><port>39100</port><rqdelay>100</rqdelay><milliseconds>true</milliseconds></command>"
tools/re/run_dll.sh 2s "$CONN" sleep:12s \
  '<command id="subscribe"><alltrades><security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>' \
  sleep:30s '<command id="disconnect"/>'
kill %1
go run ./tools/wire-decode -payload -1 ~/re/cap/session.cap > ~/re/cap/session.dump
```

- Полный XML колбэков DLL — в `$RE_WORKDIR/last-run.out`.
- Журналы DLL — в `$RE_WORKDIR/logs`: `*_xdf.log` (команды и результаты), `*_dsp.log` (состояния), `*_ts.log` (транспорт). При уровне 3 в `*_ts.log` при ошибке запроса печатается его логическое содержимое (`RQ: 1`, `WR: C:x;W:78;…`, `RD: …`) — это готовая подсказка формата. Журналы в CP1251: `iconv -f cp1251 -t utf-8`.
- Колбэк `server_status` в ответ на команду DLL выдаёт асинхронно, из рабочего потока между опросами. Если сразу за командой отправить `disconnect`, колбэк может не прийти; ставьте `sleep:2s`.

Без сервера:
- `py/sink.py 39001 out.log` — что DLL шлёт первым (RQ_LOGIN);
- `py/stub_server.py 39010 --password <пароль> --reply 50:<hex> --reply 9:<hex>` — сервер, который логинит DLL или Go-клиент и отвечает заданными payload. Он печатает каждый кадр клиента — так читаются запросы: `RQ_SUBSCRIBE` и т. д.

### RE-3. Оракул: смысл полей записи сервера

Для записей, которых демо-сервер не присылает (сделки, заявки, позиции): проигрываем DLL записанную сессию и подмешиваем сконструированные записи.

```bash
python3 tools/re/py/make_alltrades.py 1 /tmp/inject.hex      # или свой генератор по образцу
TC_PASSWORD=... python3 tools/re/py/replay_server.py ~/re/cap/session.dump 39170 --inject /tmp/inject.hex --after 10 &
tools/re/run_dll.sh 2s "<command id=\"connect\">…<host>127.0.0.1</host><port>39170</port>…</command>" sleep:8s \
  '<command id="subscribe"><alltrades><security><board>TQBR</board><seccode>SBER</seccode></security></alltrades></command>' \
  sleep:4s '<command id="disconnect"/>'
awk '/CALLBACK: <alltrades>/{p=1} p{print} /<\/alltrades>/{if(p) exit}' "$RE_WORKDIR/last-run.out"
```

- Сессия для проигрывания должна быть записана с тем же паролем: из её ответа RQ_PWD берётся сессионный ключ. Логин и подписку в команде можно менять: доказательство пароля сервер не проверяет.
- `--after` — тип запроса, после которого подмешивается payload (10 = RQ_SUBSCRIBE). До него RQ_MAIN отдаёт записанный снапшот, поэтому `secid` в подмешиваемых записях — из этой сессии.
- Меняйте по одному полю за прогон. Отсутствие сделки в XML тоже результат: так найдено правило DLL «номер сделки не больше последнего — отбросить» (`old ALLTRADE No` в `*_ts.log`).
- Для фикстуры сохраните вход (`*-inject.hex`) и ответ DLL (`*-dll.xml`). Тест Go должен выдавать этот XML побайтно.

### RE-4. Живая проверка Go

```bash
go run ./tools/native-login -addr 127.0.0.1:39100     # через proxy.py — потом wire-decode
go run ./tools/reference-harness -backend native '<command id="connect">…</command>' sleep:8s '<command id="server_status"/>'
go run ./tools/native-trades -addr … -sec TQBR:SBER -nats nats://127.0.0.1:4222
```

Критерии: сервер принимает запросы (ответ без ошибки), `Stats().UnknownRecords` не растёт после загрузки, последний кадр сессии — `RQ_LOGOUT` (тип 2).

### Go-варианты прокси и проигрывателя

Python-версии — эталон поведения; Go-версии (задача 1 плана) переносят оба инструмента на
`native/wire` и принимают те же флаги. Демо-сервер и Wine для них не нужны, только loopback.

```bash
# RE-2: прокси вместо py/proxy.py; тот же формат захвата, что читает wire-decode
go run ./tools/tcproxy -listen 127.0.0.1:39100 -upstream tr1-demo5.finam.ru:3939 -out ~/re/cap/session.cap &

# RE-3: проигрыватель вместо py/replay_server.py
TC_PASSWORD=... go run ./tools/replay-server -dump ~/re/cap/session.dump -listen 127.0.0.1:39170 \
  -inject /tmp/inject.hex -after 10
```

- `tcproxy` принимает `-listen`, `-upstream` и `-out` (файл дописывается) и пишет строки
  `"<conn> <секунды> C>S|S>C <len> <hex>"` — ровно то, что читают `../wire-decode` и
  `../replay-server`.
- `replay-server` принимает `-dump`, `-listen`, `-inject` и `-after` (по умолчанию 10 =
  RQ_SUBSCRIBE), пароль — из `TC_PASSWORD`. `-after 0` отключает подмену и проигрывает все
  записанные ответы RQ_MAIN.
- Проверка Go-версий без сети: `go test ./tools/tcproxy ./native/replay`.

## Пример: как восстанавливалась лента сделок

1. RE-2 с DLL — сделок нет (вечер), но в захвате есть `RQ_SUBSCRIBE`: `0004 0025 04 …` = `W secid, W режим, B маска` (сверено по `secid` из `<securities>`).
2. `CallArgs.java` → `RQ_SUBSCRIBE` строит `FUN_180359140`; `dll_maps.py tags` → тег `0x08 R_ALLTRADE` → `FUN_1803bf700`.
3. RE-1 `FUN_1803bf700`: базовые поля и маска флагов с необязательными полями (таблица в `docs/protocol/alltrades.md`).
4. После подписки поток останавливался на теге `0x0a` без размера → RE-1 `FUN_1803c1360` → `W secid, L` / `W secid, B`. После этого `analyze_main.py` перестал находить незнакомые теги.
5. RE-3: три набора `make_alltrades.py`. XML DLL показал: время — «настенное», `0x100` — миллисекунды, байт `0x01` — `period`, `0x08` — `buysell`, цена — как `minstep`, 64-битный номер — счётчик 7 = 8 байт, отсев старых номеров.
6. Фикстуры → тест Go побайтно → RE-4 на демо.

## Ловушки

- Go-программы не завершаются по `SIGALRM`: `perl -e 'alarm N; exec …'` не остановит `native-trades`. Останавливайте через `kill -INT` — тогда уходит `RQ_LOGOUT`.
- Вывод Wine засорён сообщениями Vulkan/MoltenVK: `run_dll.sh` ставит `WINEDEBUG=-all` и фильтрует строки.
- `secid` действителен только внутри сессии. Сравнивайте записи и XML одной и той же сессии.
- Часовой пояс сервера: время в записях — «настенное» МСК как Unix time. Блок пояса (16 байт) — в ответе RQ_TIME.
- `analyze_*.py` и `replay_server.py` читают формат `tools/wire-decode -payload -1`. Без `-payload -1` payload обрезан и помечен `…`.
- Номера функций (`FUN_…`) действительны только для этой версии DLL. При другой версии карты строятся заново скриптами из этого каталога.
