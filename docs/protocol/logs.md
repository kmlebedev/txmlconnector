# Журналы эталонной DLL (`_dsp.log`, `_xdf.log`, `_ts.log`) и их воспроизведение

Эталон — `txmlconnector64-6.47.2.26.4.dll`. Статусы: **O** — оракул (DLL под Wine 11.21, локальные
прогоны и демо-сессии только на чтение, 2026-11-05), **D** — декомпилятор (Ghidra 12.2), **I** — вывод.
Реализация — `cmd/connector/internal/dlllog` (форматы) и `cmd/connector/{main,stream}.go`
(события библиотеки); события подключения — `native.Config.Trace` (`native/trace.go`).

## 1. Файлы

| Факт | Статус |
|---|---|
| Каталог `log_path` должен существовать; иначе `Initialize`/`InitializeEx` → `<error>Не удалось инициализировать библиотеку. Указанный путь не существует</error>`. DLL каталог **не создаёт**. Завершающий `/` допустим | O |
| Имена: `<log_path>/<ГГГГММДД>_dsp.log`, `_xdf.log` (при инициализации), `_ts.log` (при первом `connect`); D: строки `%Y%m%d_xdf.log`, `%Y%m%d_dsp.log` | O+D |
| Файлы дописываются, сессии `Initialize…UnInitialize` идут подряд в одном файле | O |
| Кодировка CP1251, конец строки CRLF во всех трёх файлах | O |
| Смена файла в полночь при работающей сессии | I (реализовано: переоткрытие при смене даты) |

## 2. Формат строк

`_dsp.log` и `_xdf.log` (O):

```
HHMMSS.uuuuuu [pid %04x] [поток] <вид> текст            ← _dsp.log
HHMMSS.uuuuuu [pid %04x] [поток] <вид> [C] текст        ← _xdf.log, C ∈ V I R O
```

- Время местное, микросекунды. pid и id потока — hex, минимум 4 цифры (O: Wine-идентификаторы кратны 4).
- Поток: id вызывающего потока API; `main` — поток соединения DLL; `clbk` — поток колбэков (O).
- `_xdf.log`: `<cmd> [V] System version 6.47. TXmlConnector version 2.26.4` при инициализации;
  `<cmd> [I] команда` / `<bad_cmd> [I] текст` (не `<command>` или битый XML, в т.ч. пустая строка);
  `<res> [R] <result…>` / `<error> [R] <error>…</error>`; колбэк — `<info> [O] [Nu] xml`, где **N — время
  выполнения клиентского колбэка в микросекундах** (O: колбэк со sleep 40 мс → `[40228u]`…`[42090u]`;
  D: `FUN_1801191d0` замеряет время вокруг вызова и пишет запись после него).
- `[I]` пишется **до** выполнения команды: колбэки, пришедшие во время команды (например
  `connected="false"` при `disconnect`), стоят между `[I]` и `[R]` (O).
- Пароль в `connect`: `<password>*</password>`; логин и хост — открытым текстом (O). Наша библиотека так
  же маскирует `oldpass`/`newpass` (`change_pass`) — I, в эталоне не снималось.

`_ts.log` (O):

```
\r\n\r\nStart logging дд/мм/гггг ЧЧ:ММ:СС Transaq TXmlConnector, ver.6.47-T78-37-2.26.4\r\n
ЧЧ:ММ:СС.ммм {tid hex} текст
```

Заголовок пишется на каждую попытку соединения.

## 3. Уровни 1–3

| Запись | Уровень, с которого пишется | Статус |
|---|---|---|
| все записи `_dsp.log`, `_ts.log` | 1 | O (уровни 1 и 3 дают одинаковый `_dsp.log`) |
| `_xdf.log` `[V]`, `[I]`, `[R]` | 1 | O |
| `[O]` для `server_status`, `client`, `orders`, `positions` и прочих типов | 2 | D+O |
| `[O]` для `markets`, `boards`, `candlekinds`, `securities`, `pits`, `sec_info_upd`, `sec_info`, `overnight`, `connector_version`, `alltrades`, `quotations`, `quotes`, `candles` | 3 | D+O |
| `[O]` для `ticks` | 3 | I (та же RQ_MAIN-группа рыночных данных) |

D: `FUN_18017ffc0` возвращает уровень 3 для внутренних типов сообщения 1–7 и 2 для остальных; запись
пишется, если уровень сообщения ≤ текущего. O: демо-сессия на уровне 2 (17 типов колбэков) записала только
`server_status`, `client`, `orders`, `positions`. Типы, не встречавшиеся в сессиях, считаются уровнем 2
(ветка `default` в D).

`SetLogLevel` (O, работает и до `Initialize`):

```
<info> Changing log level. Requested level is N
<info> Log level changed successfully. New level is N        ← 1..3
<error> Log level change failed: Invalid log level. Should be in range [1,3]   ← иначе
```

## 4. Последовательности `_dsp.log` (O)

Инициализация: `Open log <log_path как передан>, level N` → `System version 6.47. TXmlConnector version
2.26.4` → `Initializing...`. Отказ: `<error> Failed to initialize library: <причина>`; при повторной
инициализации — с контекстом `[Path: p, Level: n]` (`Initialize`) или `[<исходный XML>]` (`InitializeEx`).

Соединение (поток `main`, кроме первой строки):

```
State <DISCONNECTED> changed to <CONNECTING>      (поток вызова connect)
Initializing dsp... / Preparing for login... / Packet size: 65520 / Logging in...
Logged in | <warning> Failed to login → State <CONNECTING> changed to <DISCONNECTED>
State <CONNECTING> changed to <LOADING> / Loading static data... / Loading snapshot...
State <LOADING> changed to <READY> / Main thread OK. Data refresh period: <rqdelay>
```

Отключение: `Disconnecting...` (вызов) → `Shutdown event received` (main) → `Waiting for WorkUp...` (вызов) →
`State <READY> changed to <DISCONNECTING>` → `Logging out...` → `State <DISCONNECTING> changed to
<DISCONNECTED>` → `Disconnect OK` (вызов). Без соединения `disconnect` пишет только `Disconnecting...` и
`Disconnect OK`. Порядок первых двух строк — гонка потоков, в эталоне встречаются оба.

`UnInitialize`: `Finalizing...` → `Shutdown...` → `Waiting for callback thread...` → `Shutdown OK`.

**Не воспроизводится** (I, внутреннее состояние DLL): `Snapshot loaded: Cl: N, As: M`, периодическое
`Host RAM: …`, `Command queue is empty`.

## 5. `_ts.log`

Воспроизводится (O-шаблоны): `Connecting to <host как задан>:<port> buf=65512...`, `Connected.`,
`Connection error #8, WSAE:<код>.` (O: отказ — 10061; остальные коды — Winsock-эквиваленты, I),
`Session timeout N ms.`, `Request timeout N ms.`, `SYS# <сервер>/647 PRO# <сервер>/78=0 BLD# <сервер>/2.26.4=0
SID# s UID# u`, `FORTS Date:x Current Date:<дни с 1970>`, `Trade Stream Closed. [Reason: 'Connect/Login
failed'. ]S:a/b, R:c/d, E:0, Rl:0`.

Счётчики `S`/`R`: у эталона — байты/запросы и байты/ответы; у нас — байты и число вызовов
`Write`/`Read` (I). Флаги `=0` в строке `SYS#` и `E`/`Rl` — I.

**Не воспроизводится** (I): `Server time: …`, `R_DATE:…`, `ERR Opt … not found.`, `Snapshot accepted`,
`EVT: Reload CLN.`, `Snapshot done.`, дамп статистики очереди запросов (`RQ:`, `SG:`, `RD:`, `LFT:`),
`RQ_PWD Connection closed by server - BYE`.

## 6. Без открытого журнала (O)

До `Initialize`, после `UnInitialize` и при неудачной инициализации DLL пишет те же тексты в **stdout**
(не stderr) в формате boost.log:

```
[ГГГГ-ММ-ДД ЧЧ:ММ:СС.uuuuuu] [0x%08x] [info]    текст
```

Все записи — `[info]`, даже ошибки. CP1251; конец строки — CRLF под Windows (текстовый режим CRT),
у нас на Linux/macOS — LF. Команды пишутся без префикса `<cmd> [I]`, результаты — как есть.

## 7. `logfile_lifetime` (O)

| Значение | Поведение |
|---|---|
| нет атрибута, а также `Initialize` | 7: удаляются файлы старше 7 дней по дате в имени |
| `N ≥ 0` | удаляются `ГГГГММДД_{dsp,xdf,ts}.log`, если дата в имени раньше сегодняшней больше чем на N дней (N=3: 3 дня остаются, 4 удаляются; N=0 — остаётся только сегодня) |
| `-1` (и любое отрицательное) | ничего не удаляется |
| не число (`abc`) | как 0 |

Решает дата в имени, а не время изменения файла. Файлы с другими именами (`*.bak`, `zz_dsp.log`) не
трогаются. Очистка выполняется при инициализации.

## 8. Проверка

- `go test ./cmd/connector/internal/dlllog/` — побайтные строки по образцам эталона.
- `go test ./native/ -run TestConnectorTrace` — порядок событий соединения.
- `make cabi-test` — наличие файлов и ключевых строк после C ABI-сценария.
- Живая сверка (2026-11-05, демо, только чтение): `TXC_LOG_DIR=… TXC_LOG_LEVEL=2|3 make cabi-live-test` и
  та же сессия эталона под Wine; после нормализации (время, pid/tid, секреты) `_dsp.log` и `_xdf.log`
  совпадают построчно, кроме перечисленного в §4–§5 «не воспроизводится», различий во входных командах и
  колбэка `<orders>` со снятыми за день заявками, которого `native` при загрузке не отдаёт
  (`docs/COMPATIBILITY.md` §9, A8).
