# Окружение: фактическое состояние

**Дата проверки:** 2026-11-04 · **Хост:** macOS (darwin 25.6.0, Apple Silicon arm64) ·
**Репозиторий:** `/Users/whitefox/GolandProjects/txmlconnector`, ветка `main`, HEAD `b940f9a`.
**Кто проверял:** env-scout (task-1). Все факты ниже — результат реальных команд; там, где прогона не
было, так и написано («не проверено»).

**Назначение страницы.** Ответить на два вопроса до старта любой задачи плана
[2026-11-04-native-transaq-client.md](../superpowers/plans/2026-11-04-native-transaq-client.md):
что в окружении работает и что из-за этого доступно (RE-1…RE-4, сборки, тесты), а что заблокировано —
и что делать агенту, если нужный инструмент недоступен.

**Единый способ перепроверить окружение перед задачей:**

```bash
tools/re/check_env.sh          # быстро (~1 с): версии, gofmt/vet, RE-инструменты, Wine, секреты, git
tools/re/check_env.sh --full   # + go build, go test, go test -race, кросс-сборки (~10-20 с)
```

Скрипт печатает по строке на проверку (`PASS` / `WARN` / `FAIL` / `INFO`), итоговую строку и **никогда
не печатает значения секретов**, не устанавливает ПО, не делает сетевых обращений. Код возврата `0` —
окружение пригодно (FAIL нет), `1` — есть FAIL. Статусы ниже сверяйте с его выводом; расхождение —
повод обновить эту страницу (через Lead).

Легенда: **PASS** — работает и разрешает соответствующую работу; **WARN** — работает с оговоркой или
некритично отсутствует (соответствующая работа ограничена); **FAIL** — ломает работу агента, обход
обязателен; **INFO** — справочный факт.

---

## Кратко для агента (TL;DR)

- **Go 1.27.2 darwin/arm64, CGO_ENABLED=1 на хосте.** `gofmt`, `go vet`, `go build`, `go test`,
  `go test -race` и две обязательные кросс-сборки `CGO_ENABLED=0 ./native/...` (linux/amd64, darwin/arm64;
  linux/arm64 дополнительно проверяет `tools/re/check_env.sh --full`) — **зелёные**.
- **Sandbox:** глобальный кэш Go писать нельзя → перед Go-командами `export GOCACHE="$PWD/.cache/go-build"`
  (это уже делает `make verify`). Без этого `go build/vet/test` падают с `operation not permitted`.
- **Ghidra 12.2 + openjdk@25 — есть, RE-1 доступен** при `JAVA_HOME`/`PATH` и обходе записи настроек
  Ghidra (см. [Песочница DSH](#песочница-dsh-gocache-и-запись-вне-workspace)). Полный импорт DLL не
  запускался (долго) — «не проверено».
- **Wine 11.21 установлен** (`~/re/wine/.../wine`, `~/re/wine-prefix`) — проверено: `run_dll.sh` с
  `get_connector_version` вернул `<connector_version>6.47.2.26.4</connector_version>`. **RE-2 (живой
  захват DLL) и RE-3 (оракул) доступны**; им нужны `TC_PASSWORD` и/или записанная сессия. Секреты —
  `~/re/demo.env` (шаблон `tools/re/demo.env.example`), не в репозитории.
- **Python 3.15.0, `txproto selftest: OK`** — Python-инструменты RE рабочие.
- **`TC_LOGIN` — set, `TC_PASSWORD` — unset** (значения не печатаются и не должны попадать в файлы).
  Живой логин, RE-4 и `wire-decode` с расшифровкой пока недоступны; пароль положить в
  `~/re/demo.env` (chmod 600) и загружать: `set -a; . ~/re/demo.env; set +a`.
- **Демо-сервер:** `tr1-demo5.finam.ru:3939`, 09:50–23:50 МСК, будни (кроме праздников). Любые живые
  операции — только с явного согласия пользователя в чате.
- **Эталонная DLL** в корне репозитория, sha256 совпадает с эталоном.
- **Стартовое состояние Фазы 0:** `tools/tcproxy`, `tools/replay-server`, `native/replay`,
  `native/model`, `native/records` ещё отсутствуют — это ожидаемо, их создают задачи 1–2.

---

## Таблица проверок (2026-11-04)

| Проверка | Команда | Фактический результат | Статус | Что разрешает / блокирует |
|---|---|---|---|---|
| Go toolchain | `go version` | `go version go1.27.2 darwin/arm64` (`/opt/homebrew/bin/go`) | **PASS** | сборка/тесты всех пакетов |
| CGO на хосте | `go env CGO_ENABLED` | `1` | **INFO** | норма для хоста; `native/...` обязаны собираться с `CGO_ENABLED=0` |
| Форматирование | `gofmt -l .` | пусто | **PASS** | DoD «gofmt пуст» |
| Vet | `go vet ./...` | exit 0, без диагностик | **PASS** | DoD «go vet чист» |
| Сборка текущей платформы | `go build ./...` | exit 0, ~0,6 с (тёплый кэш) | **PASS** | рабочий бинарь/пакеты |
| Тесты | `go test ./... -count=1` | exit 0, **3,84 с**: 10 пакетов `ok`, 10 `no test files` (всего 20) | **PASS** | регрессии; тесты не ходят в сеть и не требуют DLL/Wine |
| Тесты с гонками | `go test -race ./... -count=1` | exit 0, **14,04 с** | **PASS** | обязательный `-race` из AGENTS.md §7 |
| `make verify` | `make verify` | exit 0, ~1,2 с (кэш); сам подставляет repo-local `GOCACHE` | **PASS** | одна команда = gofmt/vet/build/кроссы/race |
| Кросс linux/amd64 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./native/...` | exit 0 | **PASS** | целевая платформа сервера |
| Кросс darwin/arm64 | `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./native/...` | exit 0 | **PASS** | локальная платформа |
| Кросс linux/arm64 | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./native/...` | exit 0 | **PASS** | дополнительная платформа |
| Кэш Go (sandbox) | `go env GOCACHE` + запись | `~/Library/Caches/go-build` **недоступен на запись**; с `GOCACHE="$PWD/.cache/go-build"` всё зелёное | **WARN** (обходится) | без обхода `go build/vet/test` падают; `make verify` подставляет кэш сам |
| Ghidra | `test -x /opt/homebrew/opt/ghidra/libexec/support/analyzeHeadless` | есть, **12.2 PUBLIC**; в `PATH` отсутствует | **PASS** | RE-1 (декомпиляция), при условиях ниже |
| JVM для Ghidra | `"$JAVA_HOME/bin/java" -version` | PATH `java` = заглушка `/usr/bin/java`, `JAVA_HOME` не задан; установлены **openjdk@25 (25.0.5)** и openjdk@17 (17.0.21); Homebrew-формула Ghidra 12.2 рассчитана на openjdk@25 | **PASS** (JVM 25 найдена), **WARN** (`JAVA_HOME` не задан) | `check_env.sh` находит JDK 25 сам; но для `analyzeHeadless` всё равно `export JAVA_HOME=/opt/homebrew/opt/openjdk@25/libexec/openjdk.jdk/Contents/Home` |
| Настройки Ghidra | запись в `~/Library/ghidra` | под sandbox запрещена: `LaunchSupport -save` → `FileNotFoundException ... java_home.save (Operation not permitted)` → `Unable to prompt user for JDK path, no TTY detected` | **WARN** (обходится) | обход `JAVA_TOOL_OPTIONS=-Duser.home=...` — проверено, `analyzeHeadless -help` работает |
| Скрипты Ghidra | `ls tools/re/ghidra/*.java` | 5/5: `CallArgs`, `Callers`, `ScalarRefs`, `StrRefs`, `XrefDecomp` | **PASS** | RE-1-постскрипты |
| Rosetta x86_64 | `arch -x86_64 /usr/bin/true` | exit 0 | **PASS** | macOS-Wine (x86_64) в принципе установим |
| Wine | `"$WINE" --version` (Wine 11.21 из `~/re/wine/Wine Devel.app/.../wine`); `ls ~/re/wine-prefix` | `wine-11.21`, prefix `~/re/wine-prefix` существует; `get_connector_version` через `tools/re/run_dll.sh` → `<connector_version>6.47.2.26.4</connector_version>` | **PASS** | **RE-2/RE-3 (живой DLL, оракул) доступны**; `run_dll.sh` подставляет WINE/WINEPREFIX/TXC_DLL по умолчанию |
| Python | `python3 --version` | `Python 3.15.0` (`/opt/homebrew/bin/python3`) | **PASS** | Python-инструменты RE |
| Python selftest | `python3 tools/re/py/selftest.py` | `txproto selftest: OK`, exit 0 | **PASS** | `txproto`/`dll_maps`/прокси/реплей-сервер |
| `TC_LOGIN` | `[ -n "${TC_LOGIN:-}" ]` | **set** (значение не выводится) | **PASS** | логин на демо-сервере возможен |
| `TC_PASSWORD` | `[ -n "${TC_PASSWORD:-}" ]` | **unset** (значение не выводится) | **WARN** | живой логин и `tools/wire-decode` с расшифровкой недоступны |
| `TC_DLL_PATH` / `TXC_DLL` | `[ -n ... ]` | оба **unset**, но эталонная DLL лежит в корне репо | **INFO** | `run_dll.sh` подставит `$REPO/txmlconnector64-6.47.2.26.4.dll`, если `TXC_DLL` не задан |
| `~/re/demo.env` (секреты) | `test -f ~/re/demo.env` | файла нет; шаблон — `tools/re/demo.env.example` | **WARN** | положить `TC_LOGIN`/`TC_PASSWORD` + Wine-экспорты в `~/re/demo.env` (chmod 600); значения не печатать и не коммитить |
| `TC_HOST` / `TC_TARGET` | имена переменных | set (адрес/цель демо-сервера; значения в отчёты не выводятся) | **INFO** | живой режим по расписанию, только с согласия пользователя |
| Эталонная DLL | `shasum -a 256 txmlconnector64-6.47.2.26.4.dll` | `a197e2625e53c0df453687ae1b20ef2ae1a05e034cd20c424a7014b0c9726342` — совпадает с эталоном, 10 МБ | **PASS** | RE-1/RE-2/RE-3 против правильного бинаря; DLL не модифицируется |
| Стартовые инструменты | `test -e tools/tcproxy` и др. | отсутствуют: `tools/tcproxy`, `tools/replay-server`, `native/replay`, `native/model`, `native/records` | **INFO** | ожидаемо: их создают задачи 1–2 Фазы 0 |
| Существующие инструменты | `ls tools` | есть `tools/{wire-decode,reference-harness,native-login,native-trades}` и `tools/re/{py,ghidra}` | **PASS** | вход в задачу 1 |
| Фикстуры | `find native/records/testdata -type f` | 12 файлов: `*.hex[.gz]` + эталонный `*-dll*.xml`/`*-demo.xml` | **INFO** | работа без Wine: сверка разбора (`native/records`) и XML-рендера (`native/xmlcompat`) |
| Git | `git branch --show-current`, `git status --short` | ветка `main`, HEAD `b940f9a`; незакоммичены только подготовительные правки Lead (`M .gitignore`, `M Makefile`) и новые файлы (`AGENTS.md`, `docs/deepseek/`, `tools/re/check_env.sh`) | **PASS** (см. WARN скрипта) | правок исходников нет; свои изменения агент делает в своей ветке/задаче |
| GoLand MCP: сервер | `POST 127.0.0.1:64422/stream` MCP `initialize` | отвечает: `GoLand MCP Server 2026.2.3`, протокол 2024-11-05, `mcp-session-id` выдаётся | **PASS** | сервер жив, доступен по HTTP |
| GoLand MCP: форма инструментов | `tools/list` | один tool `execute_tool` (`command` = `get_file_problems --filePath …`, `projectPath` = путь проекта); под-инструменты: `analyze_calls, get_file_problems, get_symbol_info, search_symbol, lint_files, read_file, git_status, list_directory_tree, search_file, search_text, …` | **INFO** | в сессии DSH это будет `mcp__jetbrains__execute_tool`, а не отдельные `get_file_problems` |
| GoLand MCP: привязка к проекту | `get_file_problems --filePath …/native/client.go` | сервер привязан к проекту GoLand `clickhouse-import-rosstat`; project-scoped инструменты отвечают `points to the location outside of the project directory` даже для `native/client.go`; `projectPath` проект не переключает; `read_file` и `search_symbol` по абсолютным путям работают | **WARN** | MCP видит файлы (VFS/индекс), но инспекции — только для проекта, открытого в этом окне GoLand; открыть `txmlconnector`/worktree в нём или взять MCP-порт его инстанса |
| DSH MCP-клиент | `~/.dsh/profiles/desktop/cordis.patch.yml` | патч `mcp-jetbrains` **добавлен** 2026-11-04 (бэкап `cordis.patch.yml.bak-*`); до перезапуска DSH инструментов в сессии нет | **INFO** | перезапустить DSH; после — `mcp__jetbrains__execute_tool` |
| Worktree-плагин | `create_worktree` в DSH | включён; по умолчанию создавал `.agents/worktrees/<branch>` (проверено вызовом: `/Users/whitefox/GolandProjects/txmlconnector/.agents/worktrees/<name>`, `baseCommit` = HEAD); патч `worktree.directory: .worktrees` добавлен 2026-11-04 | **INFO** | после перезапуска DSH пул — `.worktrees/<branch>`; worktree от HEAD не содержит незакоммиченных AGENTS.md/docs |

Полный вывод текущего прогона: `tools/re/check_env.sh` → `PASS=12 WARN=4 FAIL=0`, exit 0 (~1 с);
`--full` → `PASS=18 WARN=4 FAIL=0`, exit 0 (~12 с, тёплый кэш).

---

## Песочница DSH: GOCACHE и запись вне workspace

Агенты работают в песочнице DSH с политикой **workspace-write**: писать можно только в рабочее дерево
`/Users/whitefox/GolandProjects/txmlconnector` (и во временные каталоги ОС), запись за его пределы
запрещена. Это ограничение агентской песочницы, **не** проблема репозитория. Оно затрагивает три места.

### 1. GOCACHE (Go build cache) — обязательно для Go-команд

- **Симптом:** `go build ./...`, `go vet ./...`, `go test ./...` падают:
  `open /Users/whitefox/Library/Caches/go-build/<hash>: operation not permitted`;
  дополнительно может печататься warning
  `go: writing stat cache: open /Users/whitefox/go/pkg/mod/cache/download/...: operation not permitted`
  (это предупреждение **не фатально**, exit code остаётся 0).
- **Причина:** `go env GOCACHE` указывает на `~/Library/Caches/go-build`, а `GOMODCACHE` — на
  `~/go/pkg/mod`; оба пути вне workspace, sandbox запрещает запись.
- **Обход (стандарт для этого репозитория):** repo-local кэш внутри дерева

  ```bash
  export GOCACHE="$PWD/.cache/go-build"
  ```

  Каталог `.cache/` уже в `.gitignore`. `make verify` подставляет этот же кэш по умолчанию
  (`GOCACHE="${GOCACHE:-$(CURDIR)/.cache/go-build}"`), поэтому `make verify` работает «из коробки».
- **Следствие для агента:** вручную запуская `go …`, сначала экспортируй `GOCACHE`, иначе получишь
  ложный «FAIL». `tools/re/check_env.sh` делает это сам и помечает строку `WARN` с пояснением.

### 2. Каталог настроек Ghidra — для RE-1

- **Симптом:** `analyzeHeadless` (и `ghidraRun`) падают на старте:
  `java.io.FileNotFoundException: /Users/whitefox/Library/ghidra/ghidra_12.2_PUBLIC/java_home.save (Operation not permitted)`,
  затем `ERROR: Unable to prompt user for JDK path, no TTY detected.`
- **Причина:** `support/launch.sh` всегда вызывает `LaunchSupport … -save`, который перезаписывает
  `java_home.save` в `~/Library/ghidra/…` — вне workspace.
- **Обход (проверен):** каталог `user.home` JVM должен **существовать** до запуска, иначе
  `analyzeHeadless` падает с `User home directory does not exist` → `Unable to prompt user for JDK
  path`; поэтому сначала `mkdir -p`, затем export:

  ```bash
  mkdir -p "${TMPDIR:-/tmp}/dsh-ghidra-home"
  export JAVA_TOOL_OPTIONS="-Duser.home=${TMPDIR:-/tmp}/dsh-ghidra-home"
  ```

  После `mkdir -p` `analyzeHeadless -help` печатает usage; код возврата у `-help` — **1**, на него не
  ориентируйтесь (это не ошибка обхода).

  Дополнительно нужны `JAVA_HOME` (JDK 25 / openjdk@25) и `PATH` — полный рецепт в разделе
  [«Готовые команды»](#готовые-команды-копировать). Ghidra-проект держите **внутри workspace**:
  `.re-run/ghproj` (каталог `.re-run/` в `.gitignore`) — `~/re/ghproj` из README под sandbox создать
  нельзя.
- **Не проверено:** полный импорт DLL (`-import …`) не запускался — это минуты; проверены наличие
  Ghidra, JVM и старт `analyzeHeadless`. Импорт выполняет RE-инженер и фиксирует результат отдельно.

### 3. `~/re` и прочие пути вне дерева

`~/re` уже существует (создан пользователем вне агентской сессии: Wine, prefix, шаблоны). Новые
каталоги вне workspace агент по-прежнему создавать не может (`Operation not permitted`). Поэтому
артефакты RE (проект Ghidra, захваты) держите в `.re-run/` внутри репозитория или в `$TMPDIR`;
`.re-run/` уже в `.gitignore`. Wine с prefix `~/re/wine-prefix` **работает** (проверено
`run_dll.sh` + `get_connector_version`), но менять что-либо в `~/re` агенту не нужно. Сырые живые
захваты (`*.cap`, `*.dump`, `.re-run/`) в git не попадают.

---

## Готовые команды (копировать)

Базовый экспорт для любой сессии в этом окружении:

```bash
cd /Users/whitefox/GolandProjects/txmlconnector
export GOCACHE="$PWD/.cache/go-build"       # обход песочницы DSH (см. выше)
```

Проверки Go (минимум AGENTS.md §7):

```bash
gofmt -l .                                  # ожидается пусто
go vet ./...                                # без диагностик
go build ./...
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build ./native/...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./native/...
CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build ./native/...
go test ./... -count=1                      # ~4 с
go test -race ./... -count=1                # ~14 с
make verify                                 # всё перечисленное одной командой (сам ставит GOCACHE)
```

Проверка окружения одной командой:

```bash
tools/re/check_env.sh          # быстро
tools/re/check_env.sh --full   # + build/test/race/кроссы
```

Ghidra / RE-1 (пример; полный импорт DLL долгий, здесь только проверка запуска):

```bash
export JAVA_HOME=/opt/homebrew/opt/openjdk@25/libexec/openjdk.jdk/Contents/Home
export PATH="$JAVA_HOME/bin:$PATH"
export GHIDRA_HEADLESS=/opt/homebrew/opt/ghidra/libexec/support/analyzeHeadless
mkdir -p "${TMPDIR:-/tmp}/dsh-ghidra-home"     # каталог должен существовать заранее
export JAVA_TOOL_OPTIONS="-Duser.home=${TMPDIR:-/tmp}/dsh-ghidra-home"   # только под sandbox

"$GHIDRA_HEADLESS" -help                     # проверка старта (usage; exit code 1 — нормально)
mkdir -p .re-run/ghproj                      # проект внутри workspace, а не ~/re/ghproj
"$GHIDRA_HEADLESS" .re-run/ghproj txc -import "$PWD/txmlconnector64-6.47.2.26.4.dll" \
  -analysisTimeoutPerFile 3000 -max-cpu 8 -scriptPath tools/re/ghidra
```

Python / RE:

```bash
python3 --version                            # Python 3.15.0
python3 tools/re/py/selftest.py              # txproto selftest: OK
PYTHONDONTWRITEBYTECODE=1 python3 tools/re/py/dll_maps.py commands /tmp/cmdtable.c
```

Wine (RE-2/RE-3) — установлен, пути по умолчанию подставлены в `tools/re/run_dll.sh`:

```bash
export WINE="$HOME/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine"
export WINEPREFIX="$HOME/re/wine-prefix"
export TXC_DLL="$PWD/txmlconnector64-6.47.2.26.4.dll"   # или absolute path
"$WINE" --version                                       # wine-11.21
tools/re/run_dll.sh 1s '<command id="get_connector_version"/>'
# CALLBACK: <connector_version>6.47.2.26.4</connector_version>
```

Живые сессии/оракул дополнительно требуют секретов: `set -a; . ~/re/demo.env; set +a` (см. ниже).

Секреты — только наличие, значения никогда не печатать:

```bash
[ -n "${TC_LOGIN:-}" ]    && echo "TC_LOGIN: set"    || echo "TC_LOGIN: unset"
[ -n "${TC_PASSWORD:-}" ] && echo "TC_PASSWORD: set" || echo "TC_PASSWORD: unset"
[ -n "${TC_DLL_PATH:-}" ] && echo "TC_DLL_PATH: set" || echo "TC_DLL_PATH: unset"
```

Эталонная DLL:

```bash
shasum -a 256 txmlconnector64-6.47.2.26.4.dll
# a197e2625e53c0df453687ae1b20ef2ae1a05e034cd20c424a7014b0c9726342
```

---

## Чего не хватает и как ставить

Единственный источник инструкций по установке — **[tools/re/README.md §Окружение](../../tools/re/README.md#окружение)**
(там же Rosetta, Wine для macOS/Linux, Ghidra, protobuf/nats). Здесь — только краткая сводка того, чего
не хватает **сейчас**, без дублирования деталей:

| Чего нет | Что даёт | Куда смотреть / что делать |
|---|---|---|
| **Wine** — уже установлен (`~/re/wine`, `~/re/wine-prefix`) | RE-2 (живой захват DLL), RE-3 (оракул) | ничего ставить не нужно; команды — `tools/re/README.md` §Окружение, ниже |
| `JAVA_HOME` (JDK 25 уже установлен: `openjdk@25`) | запуск RE-1 без ручных `export` | `export JAVA_HOME=/opt/homebrew/opt/openjdk@25/libexec/openjdk.jdk/Contents/Home`; `export PATH="$JAVA_HOME/bin:$PATH"` |
| `TC_PASSWORD`, `TC_LOGIN` | живой логин на демо-сервере, RE-4, расшифровка захватов `wire-decode` | положить в `~/re/demo.env` (chmod 600; шаблон `tools/re/demo.env.example`); значения в файлы/логи/коммиты не вносить |
| `TXC_DLL` (или `TC_DLL_PATH`) | `tools/re/run_dll.sh` (RE-2) | не нужен: скрипт подставляет DLL из корня репозитория (переопределяется через env) |
| protobuf, nats-server, `protoc-gen-go` | фаза с NATS/protobuf (позже) | [tools/re/README.md §Окружение](../../tools/re/README.md#окружение); сейчас не требуется |
| `tools/tcproxy`, `tools/replay-server`, `native/replay`, `native/model`, `native/records` | задачи 1–2 Фазы 0 | это **работа плана**, а не установка; создаются в своих задачах |

Ничего из этого списка в task-1 не устанавливалось и не запрашивалось — фиксируется только состояние.

---

## Что заблокировано сейчас и что делать агенту

### RE-2 / RE-3 — доступны (Wine 11.21 + prefix)

`tools/re/check_env.sh` → Wine `PASS`; проверено `run_dll.sh` + `get_connector_version`. Ограничения и
порядок:

- **RE-2 (живой захват DLL):** нужен `TC_PASSWORD` (логин на демо-сервере) и окно демо-сервера;
  запись захватов — в `.re-run/` (gitignored), фикстуры — обезличенные, в `testdata/`.
- **RE-3 (оракул):** нужна **записанная сессия** (`tools/wire-decode`-дамп) и `TC_PASSWORD` для её
  сессионного ключа; без записи оракул не запускается.
- **Не подменять оракул догадками** и не выдавать вывод декомпилятора за поведение DLL; фиксировать
  статусы S/O/D/I в `docs/protocol/*`.
- Живые торговые команды — только с явного согласия пользователя (AGENTS.md §6).

### Живой логин / демо-сервер — частично заблокирован

`TC_LOGIN` есть, `TC_PASSWORD` нет → живая сессия и расшифровка захватов (`tools/wire-decode -payload`)
недоступны. Секреты: `~/re/demo.env` (chmod 600, шаблон `tools/re/demo.env.example`), загрузка —
`set -a; . ~/re/demo.env; set +a`. Правило расписания: **`tr1-demo5.finam.ru:3939`, 09:50–23:50 МСК,
будни, кроме праздников**. Любые обращения к демо-серверу — только с явного согласия пользователя в
чате.

### Живые торговые шаги — только с согласия пользователя

`neworder`, `cancelorder`, `moveorder`, `newstoporder`, `newcondorder`, `change_pass`, РПС/РЕПО —
только демо-счёт, минимальный лот, цена далеко от рынка, отмена сразу после постановки, и только после
явного согласия пользователя в чате. По умолчанию — оракул RE-3 (Wine есть; нужны записанная сессия
и `TC_PASSWORD`).

### Worktree и GoLand MCP — настроены, нужен перезапуск DSH

- Worktree: `create_worktree` по умолчанию создаёт `.agents/worktrees/<branch>`; в профиль DSH
  **добавлен** патч `worktree.directory: .worktrees` (2026-11-04, с бэкапом `cordis.patch.yml.bak-*`).
  После перезапуска DSH пул — `/Users/whitefox/GolandProjects/txmlconnector/.worktrees/<branch>`.
  Worktree от HEAD не содержит незакоммиченных `AGENTS.md`/`docs/deepseek/*` — подготовку нужно
  закоммитить.
- GoLand MCP: сервер жив, в профиль DSH **добавлен** `mcp-jetbrains`
  (`http://127.0.0.1:64422/stream`); после перезапуска DSH появится `mcp__jetbrains__execute_tool`.
  Сервер привязан к проекту GoLand `clickhouse-import-rosstat`; для инспекций Go-файлов
  `txmlconnector`/worktree проект должен быть открыт в том окне GoLand, к которому привязан MCP-порт.
  Fallback до/без MCP — `grep`/`read`/`edit` + `go vet`/`make verify`.

### RE-1 (Ghidra) — доступен, с оговорками

Ghidra 12.2 и JDK 25 есть, скрипты `tools/re/ghidra` на месте, `analyzeHeadless` стартует при
`JAVA_HOME` + обходе `user.home` (см. [Песочница DSH](#песочница-dsh-gocache-и-запись-вне-workspace)).
Ограничения: полный импорт DLL в этой проверке **не запускался**; проект Ghidra — внутри workspace
(`.re-run/ghproj`), не `~/re/ghproj`.

### RE-4 (живая проверка Go на демо-сервере) — заблокирован

Нужны `TC_LOGIN` + `TC_PASSWORD` и окно демо-сервера; сейчас `TC_PASSWORD` нет. До появления пароля
RE-4 не выполняется, сверка идёт по фикстурам.

### Стартовое состояние Фазы 0

Отсутствуют `tools/tcproxy`, `tools/replay-server`, `native/replay`, `native/model`,
`native/records` — это норма для старта: их создают задачи 1–2. До завершения Фазы 0 остальные фазы
не начинаются (AGENTS.md §10).

---

## Правила секретов

- **`TC_LOGIN`, `TC_PASSWORD`, номера счетов/клиентов/юнионов никогда не печатаются** — ни в отчётах
  агентов, ни в логах, ни в фикстурах, ни в коммитах, ни в этой документации. Допустимо только
  «set/unset» (проверка `[ -n "${TC_LOGIN:-}" ]`), без значений и без длин.
- В коде и тестах — только `os.Getenv`; живые прогоны — под env-переменными (`TC_*`, `TXML_DEMO_*`) с
  `t.Skip` при их отсутствии. Тесты не ходят в сеть и не требуют DLL/Wine.
- `tools/re/check_env.sh` проверяет **только наличие** переменных и печатает `set`/`unset`.
  `tools/re/run_dll.sh` маскирует логин/пароль в выводе, но сырой вывод (`$RE_WORKDIR/last-run.out`)
  может содержать данные счёта — он вне git (`.re-run/`, `*.cap`, `*.dump` в `.gitignore`).
- Фикстуры из живых сессий обезличиваются: клиент → `TESTCLIENT`, юнион → `TESTUNION`,
  логин → `TESTLOGIN`; длины фиксированных полей сохраняются.
- Значения секретов не запрашиваются у пользователя в чате без необходимости и не сохраняются в файлы
  репозитория; источники — только окружение процесса.

---

## Приложение: измеренные значения (2026-11-04)

| Замер | Значение |
|---|---|
| `go version` | go1.27.2 darwin/arm64 |
| `go env CGO_ENABLED` | 1 (хост); `native/...` — `CGO_ENABLED=0` |
| `go build ./...` | exit 0, ~0,6 с (тёплый кэш) |
| `go test ./... -count=1` | exit 0, 3,84 с; 10 пакетов `ok`, 10 без тестов (20 всего) |
| `go test -race ./... -count=1` | exit 0, 14,04 с |
| Кроссы `CGO_ENABLED=0 ./native/...` | linux/amd64, darwin/arm64, linux/arm64 — exit 0 |
| `make verify` | exit 0, ~1,2 с (кэш) |
| `tools/re/check_env.sh` | exit 0, PASS=12 WARN=4 FAIL=0, ~1 с |
| `tools/re/check_env.sh --full` | exit 0, PASS=18 WARN=4 FAIL=0, ~12 с |
| `python3 --version` | Python 3.15.0 |
| `python3 tools/re/py/selftest.py` | `txproto selftest: OK`, exit 0 |
| Ghidra | 12.2 PUBLIC; JDK 25.0.5; `analyzeHeadless -help` — usage, exit code **1** (с обходом `user.home`; код 1 у `-help` — норма) |
| DLL sha256 | `a197e2625e53c0df453687ae1b20ef2ae1a05e034cd20c424a7014b0c9726342` (эталон) |
| Wine | `wine-11.21` (`~/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine`), prefix `~/re/wine-prefix`; `get_connector_version` через `run_dll.sh` — `<connector_version>6.47.2.26.4</connector_version>` |
| Секреты | `~/re/demo.env` пока нет (шаблон `tools/re/demo.env.example`); `TC_LOGIN` set, `TC_PASSWORD` unset |
| DSH-профиль | 2026-11-04 добавлены `worktree.directory: .worktrees` и `mcp-jetbrains` (бэкап `cordis.patch.yml.bak-*`); применяются после перезапуска DSH |
| Git | `main` @ `b940f9a`; незакоммичены только подготовительные правки Lead (`.gitignore`, `Makefile`) и новые `AGENTS.md`, `docs/deepseek/`, `tools/re/check_env.sh` |
| Worktree (DSH) | `create_worktree` → `/Users/whitefox/GolandProjects/txmlconnector/.agents/worktrees/<name>`, default `.agents/worktrees`; probe-ветки удалены |
| GoLand MCP | `GoLand MCP Server 2026.2.3` на `127.0.0.1:64422/stream`; 1 tool `execute_tool`; привязан к `clickhouse-import-rosstat` (не к `txmlconnector`) |

**История:** 2026-11-04 — первичная проверка окружения (env-scout, task-1). При изменении окружения
обновляйте эту страницу и сверяйтесь с `tools/re/check_env.sh`.
