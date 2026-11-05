# C ABI эталонной DLL: жизненный цикл, тексты, память (C2)

Дата: 2026-11-05. Источник: **O** — оракул C ABI: DLL `txmlconnector64-6.47.2.26.4.dll`
(SHA-256 `a197e262…26342`) под Wine 11.21, харнесс `windows/amd64` (Go, `.re-run/cabi-re/harness.go`,
вне git), только локальные вызовы — сеть/сервер не использовались. **D** — декомпилятор Ghidra.
**I** — вывод. Машиночитаемые тексты/матрица — `cmd/connector/testdata/cabi-reference.json`;
сырьё — `.re-run/cabi-re/{evidence.txt,out.txt}` (вне git).

> Поправка к `docs/COMPATIBILITY.md` `2: девятый экспорт называется **`SetCallbackEx`**, а не
> «`SetLogCallbackEx`» (таблицы экспорта, D/O; `SetLogCallbackEx` в DLL нет). Также `Initialize(NULL,…)` —
> это **ошибка** (путь к каталогу логов обязателен), а «NULL» в контракте означает возврат-успех.

## 1. Экспорт (D: таблица экспорта, ординалы 0–8)

| Ord | Имя | RVA | Сигнатура (x64 stdcall = plain x64 call) |
|---|---|---|---|
| 0 | `FreeMemory` | `0x17a480` | `bool FreeMemory(BYTE* p)` |
| 1 | `GetServiceInfo` | `0x17b1a0` | `int GetServiceInfo(const BYTE* request, BYTE** response)` |
| 2 | `Initialize` | `0x17a500` | `BYTE* Initialize(const BYTE* logPath, int logLevel)` |
| 3 | `InitializeEx` | `0x17a550` | `BYTE* InitializeEx(const BYTE* xml)` |
| 4 | `SendCommand` | `0x179ec0` | `BYTE* SendCommand(BYTE* xml)` |
| 5 | `SetCallback` | `0x179d60` | `bool SetCallback(bool (*cb)(BYTE*))` |
| 6 | `SetCallbackEx` | `0x179e10` | `bool SetCallbackEx(bool (*cb)(BYTE*, void*), void* user)` |
| 7 | `SetLogLevel` | `0x17acd0` | `BYTE* SetLogLevel(int level)` |
| 8 | `UnInitialize` | `0x17aa50` | `BYTE* UnInitialize()` |

Общее: строки **UTF-8**, ответы — C-строки с NUL; освобождать `FreeMemory`. Все функции
`BYTE*` возвращают `NULL` при успехе и строку `<error>…</error>` при ошибке. Все `bool`-функции
(`SetCallback`, `SetCallbackEx`, `FreeMemory`) возвращают `true` (`1`) — валидации нет (D+O).

## 2. Состояния и взаимные исключения (O)

```
UNINITIALIZED --Initialize(log_path, level 1..3)--> INITIALIZED
UNINITIALIZED --InitializeEx(<init …/>)------------> INITIALIZED
INITIALIZED   --UnInitialize()-------------------> UNINITIALIZED
INITIALIZED   --Initialize / InitializeEx-------> error «Библиотека уже инициализирована»
UNINITIALIZED --UnInitialize()-------------------> error «Библиотека не инициализирована»
```

- Повторная инициализация **после** `UnInitialize` — успех (O: несколько циклов init/uninit подряд).
- `Initialize` и `InitializeEx` взаимно исключают друг друга в обе стороны (проверка состояния —
  до разбора XML: `InitializeEx("битый xml")` при уже инициализированной библиотеке даёт
  «уже инициализирована», а не XML-ошибку).

## 3. `Initialize` (O)

- `logPath` **обязателен**: `Initialize(NULL, level)` → `<error>Не удалось инициализировать
  библиотеку. Не указан путь к директории для хранения лог-файлов</error>`. Проверка пути идёт
  раньше проверки уровня (NULL+0 и NULL+4 дают ту же ошибку пути).
- `logLevel`: допустимы **1..3**; `0`, `4`, `-1` → `<error>… Wrong log level.</error>`.
- Успех → `NULL`. Создаёт каталог логов и файлы `<YYYYMMDD>_dsp.log` (жизненный цикл: `Open log …
  level N`, `System version 6.47. TXmlConnector version 2.26.4`, `Initializing...`, `Finalizing...`,
  `Shutdown...`, `Waiting for callback thread...`, `Shutdown OK`) и `<YYYYMMDD>_xdf.log` (поток
  команд: `<cmd>`, `<res>`, `<bad_cmd>`, `<error>`) — O (имена/строки из живых логов).

## 4. `InitializeEx` (D + O)

XML: `<init log_path="…" log_level="1..3"/>`; необязательные атрибуты `use_minidump` (по умолчанию
`false`; `"false"` — выключает minidump-обработчик) и `logfile_lifetime` (дни, D; по умолчанию 7).
Обязательны элемент `<init>` и атрибуты `log_path` и `log_level`. Точные O-тексты:

| Вход | Результат |
|---|---|
| `NULL` / `""` | `<error>Не удалось инициализировать библиотеку. параметр функции равен нулю</error>` |
| `<init log_path>` (битый) | `… Error reading Attributes.</error>` |
| `<root/>` (нет `<init>`) | `… не найден элемент &lt;init&gt;</error>` |
| `<init/>` или без `log_path` | `… не найден обязательный атрибут &lt;log_path&gt;</error>` |
| без `log_level` | `… не найден обязательный атрибут &lt;log_level&gt;</error>` |
| `log_level="0"` / `"4"` | `… Wrong log level.</error>` |
| валидный (в т.ч. `use_minidump="false"/"true"`, `logfile_lifetime="3"`) | `NULL` (успех) |

## 5. `UnInitialize` (O)

- До инициализации и повторно → `<error>Библиотека не инициализирована</error>`.
- Успех → `NULL`; останавливает поток колбэков и очередь, в лог пишет `Finalizing...`/`Shutdown...`/
  `Waiting for callback thread...`/`Shutdown OK`; новый колбэк после возврата **не приходит** (O:
  счётчик колбэков не менялся после `UnInitialize`).
- Выгрузка: после успешного `UnInitialize` `FreeLibrary(handle)` → `1` (O, харнесс-процесс).
  Без `UnInitialize` выгрузка не проверялась (**I**; по PDF обязательна).

## 6. `SetCallback` / `SetCallbackEx` (O)

- Возвращают **`true` всегда**: до `Initialize`, с `NULL`-указателем, при повторной установке (O).
- **Последняя регистрация выигрывает**: `SetCallback(cb1)` → колбэчит `cb1`; затем
  `SetCallbackEx(cb2, user=0xABCD)` → колбэчит `cb2` с переданным `user`; затем снова
  `SetCallback(cb1)` → `cb1` (O; в `cabi-reference.json` — `lifecycle.set_callback`).
- Данные колбэка клиент освобождает сам `FreeMemory`; **DLL их не освобождает** независимо от
  возвращённого `bool` (O: при return `0` и `1` доставка продолжается, двойного free не происходит,
  если клиент освобождает; при возврате `true` и освобождении в колбэке — доставка продолжается).
- Колбэк из библиотечного потока (не поток вызывающего): O — id потока колбэков постоянный и
  отличается от потока main; доставка асинхронна относительно возврата `SendCommand` (наблюдались
  колбэки в том же мс после возврата), порядок FIFO (burst из 5 команд → 5 колбэков в порядке
  отправки). `UnInitialize` дожидается потока колбэков (лог `Waiting for callback thread...`).
- Правила PDF (не запускать `Initialize`/`UnInitialize`/`SetCallback*`/`SendCommand` из колбэка;
  API не потокобезопасно) — **не проверялись** (**I**).

## 7. `SendCommand` (O)

- До `Initialize`/после `UnInitialize` → `<error>Библиотека не инициализована. Необходимо выполнить
  функцию Initialize()</error>` (в слове «инициализована» — опечатка DLL, сохранять побайтно).
- `NULL`/`""` → `<error>Error document empty.</error>`.
- Корень не `<command>` (например `<x/>`) → `<error>Only &amp;lt;command&amp;gt; tags could be
  passed to SendCommand function</error>` — **двойное экранирование** (`&amp;lt;` в UTF-8), сохранять
  как есть.
- Битый XML: `<x>` → `<error>Error reading Element value.</error>`; `<command` → `<error>Failed to
  read Element name</error>`; `<command id="x"` → `<error>Error parsing Element.</error>`.
- Валидная команда → `<result …/>`; неизвестная → `<result success="false"><message>Unknown
  command.</message></result>`; без соединения серверные команды → `<result success="false">
  <message>Cannot process this command without connection.</message></result>`.
- `get_connector_version` (без сервера) → `<result success="true"/>` + асинхронный колбэк
  `<connector_version>6.47.2.26.4</connector_version>`.
- Ошибки верхнего уровня оборачиваются в `<error>…</error>` (D: `SendCommand`), результаты команд —
  в `<result>`.

## 8. `SetLogLevel` (O)

| Уровень | Результат |
|---|---|
| 1, 2, 3 | `NULL` (успех) |
| 0, 4, -1 | `<error>Invalid log level. Should be in range [1,3]</error>` |

Работает и **до** `Initialize` (O). В лог пишет `Changing log level. Requested level is N` и
`Log level changed successfully…`/`Log level change failed: …` (O, `_dsp.log`).

## 9. `GetServiceInfo` (D + O) — не описан в PDF

```
int GetServiceInfo(const BYTE* request, BYTE** response)
request: <request><value>version|queue_size|queue_mem_used</value>…</request>
response: <service_info>…</service_info> или <error>…</error> (при code 3)
```

| Код | Значение | O-пример |
|---|---|---|
| 0 | успех, `*response` выделен | `<service_info><version>6.47.2.26.4</version></service_info>` |
| 3 | ошибка запроса/значения; `*response` = `<error>…</error>` | `Empty request`, `Empty value text`, `Unknown parameter "unknown"` |
| 4 | `request` == `NULL` (или пустая строка → трактуется как NULL при C-вызове `""`) | `*response` = `NULL` |
| 5 | `response` (второй аргумент) == `NULL` | — |
| 6 | **rate limit**: повторный вызов раньше **5000 мс** (D: `GetTickCount()-last<5000`) | `*response` = `NULL` |

- Параметры регистрозависимы: `"VERSION"` → `Unknown parameter "VERSION"`; порядок `<value>`
  сохраняется в ответе; несколько `<value>` — несколько элементов внутри `<service_info>`.
- Работает **без** `Initialize` (O): в свежем процессе `version` → код 0.
- Наблюдённые `queue_size`/`queue_mem_used` без сессии = `0`; значения при активном соединении —
  **I/S** (см. `docs/COMPATIBILITY.md` L4).
- Ответ освобождать `FreeMemory` (O: free → `1`).

## 10. `FreeMemory` (D + O)

- Возвращает **`true` всегда**; `FreeMemory(NULL)` → `true` (O).
- D: под мьютексом вызывает CRT-`free` (`thunk_FUN_1804592dc`); поэтому освобождать можно только
  указатели, выделенные библиотекой (строки `SendCommand`/`Initialize*`/`GetServiceInfo` и данные
  колбэков).
- O (Wine, осторожно): `FreeMemory(NULL)`, повторный free того же указателя и free чужого
  (Go-heap) указателя **не привели к падению** в харнессе; гарантий для реального Windows нет — **I**.
  Прямой `free()` из CRT клиента не тестировался (нельзя смешивать аллокаторы; **I**).

## 11. Потоки, память, выгрузка (сводка)

- Колбэки — отдельный поток библиотеки, FIFO, асинхронно; `UnInitialize` его дожидается и после
  возврата колбэков нет (O).
- `Initialize`/`InitializeEx`/`UnInitialize`/`SendCommand` выполняются в потоке вызывающего (O:
  id потока в `_xdf.log` совпадает с main).
- Память: всё, что вернула библиотека (включая данные колбэков), освобождает клиент через
  `FreeMemory`; DLL не освобождает данные колбэков (O).
- Выгрузка: `UnInitialize` → `FreeLibrary` = `1` (O). Для Go `c-shared` (C1) `FreeLibrary` не
  поддерживается — `UnInitialize` должен останавливать всё, а выгрузка процесса — на клиенте
  (см. `docs/COMPATIBILITY.md` `4).

## 12. D-координаты (RE-1)

`Initialize` = `0x17a500` → тонкая обёртка (`FUN_180176800`/`FUN_180176880` + `FUN_180183970`);
`InitializeEx` = `0x17a550` (TiXml-разбор, исключения → тексты); `UnInitialize` = `0x17aa50`
(`FUN_1801772f0`, `"Finalizing..."`, флаг инициализации `DAT_1809d43f4`); `SetCallback` = `0x179d60`,
`SetCallbackEx` = `0x179e10` (сеттеры `FUN_1800f4d00`/`FUN_1800f4d30`, флаг режима 0/1, user-data
`FUN_1801071d0`); `SendCommand` = `0x179ec0` (диспетчер `FUN_1800a5030`, ошибки →
`"<error>%1%</error>"`); `FreeMemory` = `0x17a480`; `SetLogLevel` = `0x17acd0`
(строка `"Invalid log level. Should be in range [%d,%d]"`, `0x18083b370`); `GetServiceInfo` =
`0x17b1a0` (rate limit 5000 мс, коды 4/5/6, `"Empty request"`, `"Empty value text"`,
`"Unknown parameter ""`, `"<service_info>"`).

## 13. I / не проверено

- Поведение на **реальном Windows** для `FreeMemory` с чужими/повторными указателями (Wine терпит).
- `use_minidump`/`logfile_lifetime` семантика (только «инициализация проходит», файлы не изучены).
- Многопоточные вызовы API (PDF: не потокобезопасно; клиент синхронизирует) — не тестировались.
- Запреты вызовов из колбэка — не тестировались (deadlock/crash не воспроизводили).
- `GetServiceInfo` `queue_size`/`queue_mem_used` с активным соединением (L4, живой сервер).
- Полная матрица `InitializeEx` с `logfile_lifetime`/`use_minidump` и разными типами XML-ошибок
  сверх перечисленных — только перечисленные выше (O), остальное — D.

## 14. Артефакты

- `cmd/connector/testdata/cabi-reference.json` — O-тексты (UTF-8 + вывод), коды, матрица состояний,
  наблюдения колбэков/потоков (для C1-тестов и C5-корпуса).
- `.re-run/cabi-re/{harness.go,run.sh,collect.sh,evidence.txt,decomp-*.c,work/logs}` — вне git.
