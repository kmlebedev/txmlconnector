# Совместимость `native` с `txmlconnector64-6.47.2.26.4.dll` и путь к собственной DLL/.so

Дата: 2026-11-05. Состояние: `main` после PR #52. Статусы фактов — как в `docs/protocol/`:
**S** — проверено на сервере (демо/прод), **O** — сверено с DLL (оракул), **D** — декомпилятор,
**I** — вывод, не проверен.

Перед релизом нужно ответить на два вопроса:

1. Насколько `native.Connector` уже ведёт себя как DLL.
2. Можно ли собрать из него свою библиотеку (`.dll`/`.so`/`.dylib`) под разные ОС, архитектуры и CPU,
   которую клиент DLL подключит без изменений и получит то же поведение.

Короткий ответ:

- **Сетевой протокол и XML уже совпадают** с DLL на демо и проде для проверенных команд.
- **Своей библиотеки пока нет.** Прототип C-обёртки собран и работает на macOS arm64/amd64 (см. §4).
  Для Windows/Linux нужны кросс-компиляторы, а для «идентичности» — набор сверочных тестов и закрытие
  пунктов §3.
- **Байтовой идентичности файла не будет и не нужно.** Цель — совпадение по контракту и поведению (§1).

---

## 1. Что значит «идентичен исходной DLL»

Совпадение бинарного файла невозможно: другой компилятор, другой рантайм, другие импорты.
Совместимость определяется уровнями. Релиз «drop-in» требует L0–L3 на каждой заявленной платформе.

| Уровень | Что совпадает | Как проверяется |
|---|---|---|
| **L0. ABI** | имена экспортов, сигнатуры, типы, соглашение вызова, владение памятью, имя файла | тест на C: загрузка через `LoadLibrary`/`dlopen`, вызов всех 9 функций |
| **L1. Жизненный цикл** | `Initialize`/`UnInitialize`, порядок и поток колбэков, поведение без `Initialize`, повторные вызовы | тест на C + сверка с DLL под Wine |
| **L2. Протокол** | байты RQ_* на проводе, разбор всех записей RQ_MAIN, восстановление сессии | реплей записанных сессий (RE-3), живые прогоны (RE-4) |
| **L3. XML** | ответы `SendCommand` и тексты колбэков побайтно (кроме сессионных `secid`, времени) | сверочный корпус: один сценарий → DLL и наша библиотека → diff |
| **L4. Нефункциональное** | файлы логов, `GetServiceInfo`, производительность, память, работа неделями | soak, бенчмарки, сравнение логов |

Различия, которые остаются **намеренно** (решение владельца — §8): строка версии, формат логов,
неподдерживаемые криптопровайдеры.

## 2. Эталон: контракт `txmlconnector64-6.47.2.26.4.dll`

- Файл: PE32+, `machine=0x8664` (Windows x64), внутреннее имя `txmlconnector64.dll`, SHA-256
  `a197e262…26342`. Импортирует 19 системных DLL Windows (WinSock, CryptoAPI, GDI+, OLE и др.), то есть
  работает только в Windows (или под Wine).
- Экспорт — 9 функций (из таблицы экспорта, **D**). PDF описывает 8: `GetServiceInfo` в нём нет.

| Функция | Сигнатура (PDF, `__stdcall`) | Поведение |
|---|---|---|
| `Initialize` | `BYTE* Initialize(const BYTE* logPath, int logLevel)` | запускает поток колбэков и логи; `NULL` — успех, иначе строка ошибки (освобождать `FreeMemory`) |
| `InitializeEx` | `BYTE* InitializeEx(const BYTE* xml)` | то же, параметры `<init log_path log_level logfile_lifetime/>`; взаимоисключающа с `Initialize` |
| `UnInitialize` | `BYTE* UnInitialize()` | останавливает потоки и логи; обязательна перед выгрузкой |
| `SetCallback` | `bool SetCallback(bool (*)(BYTE*))` | колбэк асинхронных сообщений |
| `SetCallbackEx` | `bool SetCallbackEx(bool (*)(BYTE*, void*), void* userData)` | то же с пользовательским указателем |
| `SendCommand` | `BYTE* SendCommand(BYTE* xml)` | синхронный `<result …>` или `<error>…</error>`; память — `FreeMemory` |
| `FreeMemory` | `bool FreeMemory(BYTE*)` | освобождает строки библиотеки, в том числе данные колбэка (их освобождает клиент) |
| `SetLogLevel` | `BYTE* SetLogLevel(int)` | уровни 1–3; иначе `Invalid log level. Should be in range [%d,%d]` (**D**) |
| `GetServiceInfo` | `int GetServiceInfo(const BYTE* request, BYTE** response)` (**D**) | запрос `<request><value>version\|queue_size\|queue_mem_used</value>…</request>` → `<service_info>…</service_info>`; ошибки `Empty request`, `Empty value text`, `Unknown parameter "…"`; `NULL` → код 4 |

Общие правила (PDF): строки в UTF-8; библиотека **не потокобезопасна**, вызовы API синхронизирует
клиент; из колбэка нельзя вызывать `Initialize`/`UnInitialize`/`SetCallback*`/`SendCommand`.
Колбэки приходят из одного потока библиотеки.

Тип `bool` в сигнатурах — 1 байт (C++ MSVC). На x64 `__stdcall` не отличается от обычного соглашения;
на 32-битной Windows это stdcall с очисткой стека вызываемым, и это важно для x86-сборки (§5).

## 3. Текущее соответствие `native` и DLL

### 3.1 Что уже совпадает (L2–L3)

- **Сессия:** логин, криптопрофиль, RQ_TIME/STARTUP/MAIN, восстановление RELOGIN/RECOVER, коды отказа
  в BYE с текстами DLL (**S/O/D**).
- **Загрузка:** справочники, инструменты, клиенты, позиции, юнионы, сообщения; на проде все 397 ответов
  RQ_MAIN разбираются, состав и число колбэков совпадают с DLL (**S**, 2026-11-05).
- **Команды:** все 45 имён таблицы команд DLL обрабатываются (`connector_matrix_test.go`). Побайтно
  сверены, в том числе на проде: `connect`, `server_status`, `get_securities`, подписки,
  `gethistorydata`, сообщения и новости, опционы (`<option_families>`, `<family_strikes>`),
  `get_cln_sec_permissions` с шаблоном маржи, тексты ошибок (`&quot;`).
- **Торговые команды:** тела `neworder`/`cancelorder`/`moveorder` сняты на демо (**S**), `makeordown`
  сверен оракулом (**O**). На проде торговых команд не было.
- **Устойчивость:** soak демо 5 мин и прода 2,5 мин без неизвестных записей и дублей.

### 3.2 Что не совпадает или не проверено

| # | Область | Состояние | Уровень |
|---|---|---|---|
| 1 | **C ABI** | **закрыто** (C1, §9): `cmd/connector`, 9 экспортов, ординалы как у эталона | L0 ✓ |
| 2 | `InitializeEx`, `GetServiceInfo`, `SetLogLevel` | **закрыто** по текстам и кодам (C2, корпус 52/52); `SetLogLevel` уровень не меняет — логов нет (A1) | L1 ✓ |
| 3 | **Логи** (`<дата>_dsp.log`, `_xdf.log`, `_ts.log`, уровни 1–3, `logfile_lifetime`) | **закрыто** в формате DLL (C6.1, [protocol/logs.md](protocol/logs.md)); не воспроизводятся внутренние строки `Host RAM`, `Snapshot loaded`, статистика `_ts` | L4 ✓ (частично) |
| 4 | Строка версии | **закрыто**: библиотека отдаёт `6.47.2.26.4` (решение 1) | L3 ✓ |
| 5 | Тексты ошибок самой обёртки (`Initialize` дважды, вызов до `Initialize`, `<error>`) | **закрыто**: сняты под Wine (**O**) и повторены (C2) | L1 ✓ |
| 6 | Прокси в `<connect>` (SOCKS4/SOCKS5/HTTP-CONNECT) | DLL поддерживает (**D**), `native` — нет | L2 |
| 7 | Криптопровайдеры/сертификаты (RQ_HANDSHAKE 106, `authenticate`, `set_csp`) | только встроенный профиль; `authenticate` — частично | L2 |
| 8 | Права/ставки без шаблона маржи (демо: `regratelong` 14.28) | не воспроизведено | L3 |
| 9 | `get_united_portfolio/equity/go`, `newmrepoorder`, `newreport4` | заглушки/частично (**I**) | L3 |
| 10 | РПС `<negdeal>`/`<report>`, `<options_failed>` | не подключены к разбору (**I**) | L3 |
| 11 | Возврат `false` из колбэка | у DLL смысл не снят (**I**) | L1 |
| 12 | **Windows-сборка `native` не запускалась ни разу**, только кросс-компиляция | не проверено | L0–L3 |
| 13 | Длительная работа (торговый день и дольше) | не проверено | L4 |

## 4. Прототип C-обёртки (проверено 2026-11-05)

Прототип — Go-пакет `main` с `//export` функциями поверх `native.Connector`,
`go build -buildmode=c-shared`; код вне git: `.re-run/cabi/`. Реализует `Initialize`, `UnInitialize`,
`SetCallback`, `SetCallbackEx`, `SendCommand`, `FreeMemory`, `SetLogLevel` (заглушка).

| Проверка | Результат |
|---|---|
| Сборка macOS arm64 и amd64 (`CGO_ENABLED=1`, clang) | библиотеки по 4,3–4,6 МБ, экспорты есть |
| Вызов из C (arm64; amd64 через Rosetta) | `get_connector_version`, `server_status` и колбэк — как у нативного коннектора |
| Полный цикл на демо через C ABI | connect → `connected=true` → `get_securities` → disconnect, 764 колбэка, данные освобождаются через `FreeMemory` |


### 4.1 Формализованный live C ABI-тест (C4, 2026-11-05)

Прототип перенесён в репозиторий:

- харнесс: [`cmd/connector/testdata/abi_live.c`](../cmd/connector/testdata/abi_live.c) — env-gated
  (`TC_HOST/TC_PORT/TC_LOGIN/TC_PASSWORD`), read-only: `Initialize` → `SetCallback` → `connect` →
  ожидание `connected="true"` → `get_securities` → `disconnect` → `UnInitialize`;
- цель Makefile: `make cabi-live-test` (сначала `source ~/re/demo.env`).

**Evidence (macOS arm64, нативный C harness, dlopen `bin/libtxmlconnector.dylib`):**
`connected=true after 761 callbacks`, затем `LIVE ABI OK: … callbacks=763` — connect/get_securities/
disconnect/UnInitialize success; торговых команд нет. (Тот же сценарий ранее вручную повторил Lead.)

Выводы:

- Подход работает: Go-рантайм внутри библиотеки, колбэки из одного закреплённого потока
  (`runtime.LockOSThread`), строки из `malloc`, освобождение через `FreeMemory`.
- **c-shared требует cgo и C-компилятор под каждую целевую платформу.** Пакеты `native/...` остаются
  чистым Go; cgo нужен только тонкому слою экспорта.
- Под Windows и Linux прототип не собирался: на машине нет кросс-компилятора (zig, mingw-w64), а
  скачивать его без согласия нельзя.

## 5. Платформы и сборка

| Цель | Файл | Тулчейн | Особенности |
|---|---|---|---|
| Windows x64 (главная) | `txmlconnector64.dll` | mingw-w64 или `zig cc -target x86_64-windows-gnu` | drop-in замена эталона; `GOAMD64=v1` для старых CPU |
| Windows x86 | `txmlconnector.dll` | `i686-w64-mingw32` | нужны `__stdcall`-обёртки на C и `.def` с недекорированными именами; это отдельная работа |
| Windows arm64 | `txmlconnector64.dll` (arm64) | llvm-mingw / zig | у эталона нет arm64-сборки: сравнивать не с чем, только с x64 |
| Linux amd64/arm64 | `libtxmlconnector.so` | gcc/clang, zig, контейнер | у DLL нет Linux-аналога: «идентичность» — по XML (L3), ABI — тот же набор функций |
| macOS arm64/amd64 | `libtxmlconnector.dylib` | Xcode clang | проверено прототипом; для распространения нужна подпись/нотаризация |


### 5.0 Сборка через Zig (C9, 2026-11-05)

С C9 C-тулчейн для всех целей — **Zig 0.17.1** (`zig cc -target …`), решение владельца 4.
Скрипт [`tools/cabi/zig_build.sh`](../tools/cabi/zig_build.sh), цель `make cabi-zig` (вывод `bin/zig/<target>/`):

| Цель | `-target` | Где собирается | Особенности |
|---|---|---|---|
| linux-amd64 / linux-arm64 | `x86_64-linux-gnu.2.17` / `aarch64-linux-gnu.2.17` | любой хост | `.so` требует glibc ≥ 2.17 (CentOS 7 и новее) |
| windows-amd64 | `x86_64-windows-gnu` | любой хост | после линковки `tools/cabi/pe-repro` обнуляет метки времени PE и GUID CodeView, `go tool buildid -w` пересчитывает build ID |
| macos-arm64 / macos-amd64 | `aarch64-macos` / `x86_64-macos` | только macOS | рантайм Go линкует `libresolv` — нужен macOS SDK (`xcrun`/`SDKROOT`) |

Проверено локально (macOS arm64, Go 1.27.2, Zig 0.17.1):

- все 5 целей собираются с одного хоста;
- **сборки воспроизводимы**: два прогона в разные каталоги дают одинаковый SHA-256 для всех 9 файлов;
  без `pe-repro` отличалась только Windows-DLL;
- Windows-DLL: экспорт — 9 имён, ординалы 1–9 как у эталона; subsystem GUI, как у эталона (A7);
  импорт — `KERNEL32` и UCRT (`api-ms-win-crt-*`);
- Linux `.so`: максимальная версия glibc в символах — 2.17 (arm64) и ниже (amd64);
- `abi_test` и конформанс 52/52: `.dylib` arm64 (нативно) и amd64 (Rosetta), Windows-DLL под Wine 11.21;
- **живая сессия Windows-DLL** под Wine на демо (только чтение): connect → 761 колбэк → `get_securities`
  → disconnect → `UnInitialize`; журналы совпадают с эталоном так же, как у macOS-сборки.

Не проверено локально: запуск Linux `.so` (нет Linux-хоста) — это делает CI на ubuntu-latest,
ubuntu-24.04-arm и в контейнере `centos:7`; запуск на настоящем Windows — CI windows-latest.

### 5.1 CI-сборка (C3, 2026-11-05; с C9 — через Zig)

С C9 workflow устроен иначе, чем описано ниже для C3: job `build-cross` (ubuntu) кросс-собирает linux и
windows через Zig, проверяет воспроизводимость, glibc ≤ 2.17 и экспорт Windows-DLL; job `test` гоняет
`abi_test` и конформанс на родных раннерах (linux amd64/arm64, **windows — строго, не best-effort**,
плюс конформанс в `centos:7`); job `macos` собирает и тестирует macOS (amd64 через Rosetta).

**CI-прогон C9 (2026-11-05, run 42047301619, workflow_dispatch на `acceptance/cmd-connector`) — все
задания SUCCESS:** повторная сборка побайтно совпала для всех 9 файлов (linux, windows, macos); glibc ≤ 2.17;
экспорт Windows-DLL совпал с эталоном (`pe-repro -exports`); `abi_test` + конформанс 52/52 на linux-amd64,
linux-arm64, **windows-amd64 (настоящий Windows)**, macos-arm64, macos-amd64 (Rosetta) и в `centos:7`
(glibc 2.17). Первый прогон (42045489378) упал только на разборе вывода GNU objdump — шаг переписан.
Ниже — исторический раздел C3.

Workflow [`.github/workflows/compat-release.yml`](../.github/workflows/compat-release.yml) собирает
артефакты на нативных GitHub Actions runners:

| Job | Runner | Артефакт | ABI-тест |
|---|---|---|---|
| linux-amd64 | ubuntu-latest | `libtxmlconnector.so` + `libtxmlconnector64.so` | да |
| linux-arm64 | ubuntu-24.04-arm | `libtxmlconnector.so` + `libtxmlconnector64.so` | да |
| windows-amd64 | windows-latest + msys2/mingw | `txmlconnector64.dll` | best-effort |
| macos-arm64 | macos-14 | `libtxmlconnector.dylib` + `libtxmlconnector64.dylib` | да |
| macos-amd64 | macos-13 | `libtxmlconnector.dylib` + `libtxmlconnector64.dylib` | да |

Каждый build-job: `go test ./cmd/connector/...`, `go build -buildmode=c-shared -trimpath -buildvcs=false`,
C ABI-тест, C5 conformance, упаковка **одного архива на target** и `upload-artifact`. Архив содержит
канонические имена библиотек, `txmlconnector.h`, `VERSION.txt` (drop-in version/commit/date/target)
и README; имена: `txmlconnector-<target>.tar.gz` (linux/macos) / `.zip` (windows).

На тег `compat-v*` отдельный job `release` (ubuntu-latest, `needs: build`) скачивает все 5
артефактов, формирует один `SHA256SUMS` по архивам и публикует **один** GitHub Release
(`softprops/action-gh-release`, `generate_release_notes`, `prerelease=true` для `*rc*`). Это
устраняет гонку, когда каждый matrix-job создавал один и тот же Release (C3.1). Локально те же шаги:
`make cabi` (текущая ОС), `make cabi-test`, `make cabi-conformance`.

**Первый CI-прогон (2026-11-05, run 42023156690):** linux-amd64, linux-arm64, macos-arm64,
windows-amd64 — успешно (включая C ABI-тесты; windows mingw best-effort — success). macos-amd64
не стартовал: `macos-13` недоступен на public runners, поэтому workflow переведён на cross-сборку
`GOARCH=amd64 CC="clang -arch x86_64"` на macos-14 + Rosetta best-effort; нужен повторный прогон.

Ограничения Go c-shared, которые надо учесть в дизайне:

- В процессе может работать только один Go-рантайм. Две разные Go-библиотеки в одном процессе
  конфликтуют. Для клиентов на C#, Delphi или Python это не важно, для Go-клиентов важно (им лучше
  подключать `native` напрямую).
- Выгрузка библиотеки (`FreeLibrary`/`dlclose`) у Go c-shared не поддерживается. `UnInitialize` должен
  останавливать работу, а сама библиотека остаётся в памяти до выхода процесса. У DLL выгрузка
  разрешена — это отличие нужно задокументировать.
- `GOAMD64=v1` и `GOARM64=v8.0` обеспечивают работу на любых CPU своей архитектуры. Более высокие
  уровни — только по решению владельца.
- Сборки воспроизводимые (`-trimpath`, фиксированная версия Go, `-buildvcs`), чтобы хеш файла
  проверялся при релизе.

## 6. Как доказать идентичность перед релизом

1. **Сверочный корпус (golden).** Набор сценариев: последовательности команд плюс записанные сессии
   сервера (`tools/replay-server`, RE-3). Для каждого сценария снимается выход эталонной DLL под Wine
   (`tools/re/run_dll.sh`). В git — только обезличенные фикстуры.
2. **Дифференциальный прогон.** Тот же сценарий прогоняется на нашей библиотеке через тот же
   C-harness. Сравнение побайтное, с нормализацией только `secid` и времени.
3. **ABI-тест на C** для каждой платформы: загрузка, все 9 экспортов, ошибочные вызовы, освобождение
   памяти, отсутствие утечек (ASan/Valgrind на Linux).
4. **Windows вживую:** наша `.dll` подставляется вместо эталона в существующий `server/` (`TC_BACKEND=dll`)
   и в стороннего клиента (например, на C#). Прогоняются RE-4 и soak.
5. **CI-матрица:** workflow `compat-release.yml` (§5.1) собирает все цели §5 и запускает C ABI-тест
   на linux/macos; Windows ABI-тест — best-effort до C4. Проверка артефакта: `sha256sum -c SHA256SUMS`
   внутри архива; заголовок — `txmlconnector.h`. Wine-сверка с эталоном — отдельный job (C5).
6. **Архивы релиза (C3.1):** на каждый target — `txmlconnector-<target>.{tar.gz,zip}` с
   каноническими библиотеками, header, `VERSION.txt`, README; `SHA256SUMS` и пять архивов — в одном
   GitHub Release. Проверка: `sha256sum -c SHA256SUMS` после скачивания.
7. **Live C ABI-тест:** `make cabi-live-test` (§4.1) на demo.env — read-only connect → connected=true →
   get_securities → disconnect; macOS arm64 PASS (761 callback).
8. **Проверка CI-артефактов (2026-11-05):** скачаны архивы linux-amd64/arm64, macos-arm64,
   windows-amd64. `sha256sum -c SHA256SUMS` — OK; `file` — ELF x86-64/aarch64, Mach-O arm64,
   PE32+ DLL x86-64; экспорты: 9/9 в `nm -D` (linux), `nm -gU` (macOS), `objdump -p` (windows).
   Linux arm64 в CI проверен нативным runner (ABI-тест success).

**C5 conformance-корпус (2026-11-05).** Машиночитаемая часть §6.1–6.2 реализована как
`cmd/connector/testdata/cabi_scenarios.json` (52 упорядоченных локальных шага: lifecycle, тексты
обёртки, `GetServiceInfo` коды 0/3/4/5/6 с 5-секундным rate-limit, порядок/поток колбэков) и
C-харнесс `cmd/connector/testdata/abi_conformance.c`: он загружает нашу `.so`/`.dylib`/`.dll`
через `dlopen`, проигрывает сценарии и сравнивает возвращаемые UTF-8 строки **побайтно** с текстами
эталонной DLL из `cabi-reference.json` (C2). Запуск — `make cabi-conformance` (локально
macOS/Linux); в CI шаг «C ABI conformance» добавлен для linux/macos (strict) и windows (best effort).
Сценарии только локальные: сеть/сервер не используются, `{LOG_DIR}` нормализует временный путь
логов. Расхождения печатаются с ожидаемой/фактической строками и hex-дампами и блокируют релиз до
синхронизации C2.1 (task-73).

Критерий релиза: L0–L3 зелёные на Windows x64 (главная цель) и на каждой заявленной платформе,
открытых пунктов §3.2 уровня L0–L1 нет, оставшиеся L3–L4 перечислены в release notes.

## 7. Дорожная карта

| Этап | Результат | Критерий готовности |
|---|---|---|
| **C1. ABI-слой** | **готово**: `cmd/connector` (cgo, 9 экспортов), `include/txmlconnector.h`, `Makefile`-цели `cabi`/`cabi-so`/`cabi-dylib`/`cabi-test`; C-харнесс dlopen/LoadLibrary | ABI-тесты PASS: macOS arm64/amd64 (локально/CI), linux amd64/arm64 (CI), Windows ABI best-effort |
| **C2. Жизненный цикл как у DLL** | **готово**: точные O-тексты DLL сняты под Wine и повторены (Initialize/InitializeEx/UnInitialize/SetCallback*/SendCommand/SetLogLevel/GetServiceInfo/FreeMemory); rate-limit 5000мс; один поток колбэков | L1-сверка по `cabi-reference.json` зелёная; C2.1 sync + C5 conformance 52/52 |
| **C3. Кросс-сборка** | **готово**: `compat-release.yml` — 5 нативных целей (linux amd64/arm64, windows amd64, macos arm64/amd64), per-target архивы + header/VERSION, единый release-job, SHA256SUMS, prerelease для rc | RC2 `compat-v1.0.0-rc2`: все 5 build + release job SUCCESS, `sha256sum -c OK`, арх-проверка (ELF x86-64/aarch64, PE32+, Mach-O arm64/x86-64) |
| **C4. Живая проверка библиотеки** | **готово для macOS**: live C ABI через `dlopen` на демо (connect → 761 колбэк → get_securities → disconnect → UnInitialize, read-only), `make cabi-live-test` (харнесс исправлен при приёмке §9: после C2 `Initialize(NULL)` — ошибка, нужен каталог логов); linux amd64/arm64 ABI+conformance в CI; Windows DLL собран, экспорты 9/9, **живой Windows-прогон — открыт** (нужен mingw/согласие) | macOS live PASS; остальные платформы — CI; Windows live — этап C8/по согласию |
| **C5. Сверочный корпус** | **готово**: `cabi_scenarios.json` (52 упорядоченных шага из `cabi-reference.json`), C-харнесс `abi_conformance.c` (побайтное UTF-8 сравнение, hex-diff, колбэки/поток/user-data), `make cabi-conformance`, CI strict linux/macos | 52/52 steps passed, 0 failed; `make cabi-test` PASS |
| **C6. Закрытие §3.2** | портфели `united_*`, РПС-события, ветка без шаблона маржи; прокси/CSP — документированы как неподдерживаемые | каждый пункт — S/O-сверка |
| **C6.1. Логи как у DLL** — **готово** (2026-11-05, [protocol/logs.md](protocol/logs.md)) | `Initialize`/`InitializeEx` открывают `<log_path>/<YYYYMMDD>_dsp.log`, `_xdf.log`, `_ts.log` в формате эталона (`HHMMSS.uuuuuu [pid] [thread] <kind> …`, `<cmd> [I]`/`<res> [R]`/`[O]` в `_xdf`); уровни 1–3 и `SetLogLevel` с текстами `Changing log level…`; `logfile_lifetime`; пароль в `connect` маскируется | побайтные тесты `dlllog` по образцам эталона; живая сверка на демо (уровни 2 и 3) с эталоном под Wine — `_dsp`/`_xdf` совпадают построчно, кроме перечисленного в logs.md §4–§5 |
| **C9. Сборка через Zig** (решение 4) — **готово** (CI run 42047301619 зелёный) | `compat-release.yml` и `Makefile`: `CC="zig cc -target <triple>"` для linux amd64/arm64 (glibc с фиксированной версией), windows amd64, macOS arm64/amd64; один хост может собрать все цели | RC-сборка через Zig: ABI-тест + конформанс 52/52 на каждой цели, экспорты/ординалы 1–9 как у эталона |
| **C7. Длительная работа** | soak на торговый день на демо и проде (только чтение) | без роста памяти и неизвестных записей |
| **C8. x86 / arm64** | stdcall-обёртки и `.def` для x86; arm64-сборки | ABI-тест и корпус на каждой |

Порядок: C1 → C2 → C3 → C4 обязательны для первого релиза под Windows x64. C5 идёт параллельно с
C2–C4. C6–C8 — по решениям из §8.

## 8. Решения, которые нужны от владельца

1. **Строка версии:** отдавать `6.47.2.26.4` как DLL (полная подмена) или `6.47.2.26.4-native` (честная
   метка, но клиент может её проверять).
2. **Логи:** повторять файлы и формат DLL или писать свой формат. Это влияет на поддержку у брокера.
3. **Платформы первого релиза:** только Windows x64 или сразу Linux/macOS; нужна ли x86.
4. **Тулчейн:** установка zig или контейнера с mingw-w64 (скачивание — только с согласия).
5. **Прокси и криптопровайдеры:** входят в первый релиз или документируются как неподдерживаемые.
6. **Выгрузка библиотеки:** принять ограничение Go (`FreeLibrary` не освобождает рантайм) или
   выносить коннектор в отдельный процесс (локальный gRPC-сервер уже есть).
**Уточнение владельца при приёмке `cmd/connector` (2026-11-05, заменяет ответы ниже там, где они
расходятся):**

1. Версия — оригинальная `6.47.2.26.4` (совпадает с прежним ответом).
2. **Логи — в оригинальном формате DLL** (`<дата>_dsp.log`, `_xdf.log`, `_ts.log`), а не свой формат.
3. Платформы — основные популярные (текущие пять целей; x86/arm64 Windows — C8).
4. **Тулчейн — Zig** (`zig cc` как C-компилятор для cgo на всех целях), а не gcc/mingw/clang runners.
5. Прокси и внешние криптопровайдеры — не входят в релиз (совпадает).
6. Отдельного процесса не делаем, ограничение выгрузки Go c-shared принимается (совпадает).

Следствия — этапы C6.1 (логи) и C9 (Zig) в §7, итог приёмки — §9.

**Ответы владельца (2026-11-05, курс на drop-in для palmshell, первая редакция):**

1. **Версия — идентичная `6.47.2.26.4`** по умолчанию (drop-in), переопределение через env
   `TXC_VERSION`; настоящая версия/метка сборки — в логах и метаданных артефакта.
2. **Логи:** на первом релизе свой формат (L4), имена файлов при необходимости совместимого вида —
   отдельным этапом C6; клиент не должен от них зависеть.
3. **Платформы первого релиза:** **linux amd64+arm64, windows amd64, macOS arm64+amd64**;
   Windows x86 и Windows arm64 — этап C8.
4. **Тулчейн:** нативные GitHub Actions runners (ubuntu — gcc, windows — mingw, macOS — clang),
   локально кросс-компиляторы не скачиваем.
5. **Прокси и внешние криптопровайдеры:** вне первого релиза — документируются как неподдерживаемые
   (C6), проверяется на palmshell.
6. **Выгрузка:** принимаем ограничение Go c-shared: `UnInitialize` останавливает работу и потоки,
   сама библиотека остаётся в адресном пространстве до выхода процесса; в документации для palmshell
   это фиксируется.

**Имена артефактов:** `libtxmlconnector.so` (+ совместимый симлинк `libtxmlconnector64.so`),
`txmlconnector64.dll`, `libtxmlconnector.dylib`; заголовок `txmlconnector.h` — в каждом архиве.

## 9. Приёмка `cmd/connector` (2026-11-05, `main` 8f189aa)

Проверял Claude по `main` после PR #54–#56. Секреты не печатались, прод не использовался, торговых
команд не было.

### 9.1 Что проверено и прошло

| Проверка | Команда / источник | Результат |
|---|---|---|
| Полный набор проверок репозитория | `make verify` (gofmt, vet, build, кроссы linux/darwin, `go test -race ./...`) | exit 0 |
| Go-тесты обёртки | `go test -race -count=1 ./cmd/connector/...` | ok |
| C ABI-тест | `make cabi-test` (macOS arm64) | `C ABI conformance OK` |
| Сверочный корпус C5 | `make cabi-conformance` | 52/52 |
| То же на реальном Windows | CI `compat-v1.0.0-rc2`, job windows-amd64 | ABI-тест и конформанс 52/52 |
| Экспорт Windows-DLL | `objdump -p` артефакта RC2 и эталона | 9 имён, ординалы 1–9 **совпадают**, PE32+ x86-64 |
| Экспорт `.dylib` | `nm -gU` | 9 функций API + служебные символы cgo (`_cgo_*`, `crosscall*`); на API не влияют |
| Живая сессия через C ABI | `make cabi-live-test` на демо, только чтение | connect → `connected=true` после 761 колбэка → `get_securities` → disconnect → `UnInitialize`, 763 колбэка |
| Нагрузка и жизненный цикл | C-тест: 50 циклов Initialize/UnInitialize × 8 потоков × 1000 `SendCommand` | 200 000 колбэков из 200 000, maxrss 15 МБ стабильно, после `UnInitialize` колбэков нет |
| Повторный вход из колбэка | `SendCommand` внутри колбэка | работает (`Cannot process this command without connection.`) |
| Решения 1, 3, 5, 6 | код и CI | версия `6.47.2.26.4` (env `TXC_VERSION`), 5 целей, прокси/CSP нет, выгрузка — ограничение принято |

### 9.2 Что не прошло или не соответствует решениям

| # | Находка | Серьёзность | Что делать |
|---|---|---|---|
| A1 | **Логов нет совсем**: `Initialize` создаёт каталог, но ничего не пишет; `log_level`, `SetLogLevel`, `logfile_lifetime` игнорируются. По решению 2 нужен оригинальный формат | **закрыто** (C6.1) | журналы в формате DLL; заодно: несуществующий `log_path` теперь ошибка «Указанный путь не существует», как у DLL (раньше каталог создавался молча) |
| A2 | **Сборка не через Zig**: CI использует gcc/mingw/clang (решение 4 — Zig) | **закрыто** (C9, §5.0–5.1, CI зелёный) | `tools/cabi/zig_build.sh`, новый `compat-release.yml` |
| A3 | `make cabi-live-test` падал на `main`: харнесс вызывал `Initialize(NULL, 1)`, что после C2 — ошибка, как у DLL | исправлено в этой ветке | `abi_live.c` передаёт каталог логов (`TXC_LOG_DIR`, по умолчанию `bin/abi-live-logs`) |
| A4 | `UnInitialize` **из колбэка** подвешивает поток колбэков навсегда (ждёт сам себя), вызов не возвращается. PDF такой вызов запрещает, поведение DLL не снято (I) | P3 | вернуть `<error>` вместо зависания или снять поведение DLL (RE-2) |
| A5 | `GetServiceInfo` отдаёт `queue_size`/`queue_mem_used` всегда `0`; доставка колбэков идёт через буфер на 1024 сообщения с блокировкой отправителя | P3 | считать реальную очередь (L4) |
| A6 | Windows-DLL зависит от UCRT (`api-ms-win-crt-*`): Windows 10+ из коробки, на 7/8.1 нужен UCRT. Живая сессия: под Wine — PASS (C9); на настоящем Windows — не запускалась | P3 | указать в README релиза; живой прогон на Windows — C8 |
| A7 | Поле subsystem у нашей DLL — console, у эталона — GUI; на загрузку DLL не влияет | **закрыто** (C9: с Zig subsystem GUI) | — |
| A8 | При загрузке `native` не отдаёт `<orders>` с заявками дня в конечных статусах (O: DLL на демо отдаёт 4 снятые заявки одним колбэком). Найдено сверкой журналов | P2 | отдельная задача в `native` (разбор/вывод заявок снапшота) |
| A9 | Обёртка возвращала результат `disconnect` раньше колбэка `connected="false"` (колбэки шли через промежуточную очередь); у DLL колбэк приходит до возврата | **закрыто** (C6.1) | колбэк вызывается из потока доставки коннектора |

### 9.3 Вердикт

`cmd/connector` **принят по ABI и поведению обёртки** (L0–L1 и живая сессия L2–L3 на macOS; L1 на
реальном Windows через CI). **Как релиз `1.0.0` не принят** до закрытия A1 (логи в формате DLL) и A2
(сборка через Zig); A3 исправлен здесь, A4–A7 — не блокируют.

**Обновление 2026-11-05 (C6.1):** A1 и A9 закрыты. Открыты: A2 (Zig) — блокер релиза; A8 (`<orders>` при
загрузке) — P2.

**Обновление 2026-11-05 (C9):** A2 и A7 закрыты (Zig, воспроизводимые сборки 5 целей, CI зелёный на всех
родных раннерах, включая настоящий Windows и `centos:7`; живая сессия Windows-DLL — под Wine). Блокеров
релиза из решений владельца не осталось; открыты A8 (P2) и A4–A6 (P3).

