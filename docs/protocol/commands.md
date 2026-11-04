# TRANSAQ: XML-команды нативного коннектора

Дата: 4 ноября 2026. Источник: `txmlconnector64-6.47.2.26.4.dll`, демо-сервер Finam. Статусы: **S** — сверено с DLL на демо-сервере, **D** — прочитано в декомпиляторе, **I** — вывод.

Реализация: [`native.Connector`](../../native/connector.go) — интерфейс [`connector.Connector`](../../connector/connector.go), как у DLL: XML-команда на входе, `<result>` на выходе, остальные сообщения — в callback.

## Как DLL обрабатывает команды

- Экспорт `SendCommand` (`0x179ec0`) вызывает диспетчер `FUN_1800a5030`. Он ищет команду по `id` в таблице, которую заполняет `FUN_1801774b0` (43 имени), — **D**.
- Неизвестный `id` даёт `<result success="false"><message>Unknown command.</message></result>` — **D**, **S**.
- У каждой команды есть флаг «нужно соединение». Без соединения ответ — `Cannot process this command without connection.` — **D**, **S**. «Соединение» означает завершённую загрузку: во время логина и снапшота DLL отвечает так же — **S**.
- Обработчик возвращает признак успеха и необязательные атрибуты. Успех — `<result success="true"%1%/>`, ошибка — `<result success="false"><message>…</message></result>` — **D**.
- Сетевых запросов `server_status` не делает: обработчик (`FUN_180197de0` → `FUN_1800f0f80`) отправляет рабочему потоку сообщение `0x432` и сразу возвращает успех. Поток в своём цикле (`FUN_1800f49d0`, между опросами RQ_MAIN) формирует колбэк `<server_status>` (`FUN_1800f5370`) — **D**, **S**.

## Поведение команд (эталон DLL на демо-сервере)

| Ситуация | Ответ | Колбэк |
|---|---|---|
| `server_status` без соединения / во время логина | `Cannot process this command without connection.` | — |
| `server_status` после подключения | `<result success="true"/>` | `<server_status sys_ver="626" build="4" server_tz="Russian Standard Time" id="0" connected="true"/>` (асинхронно) |
| `connect` | `<result success="true"/>` сразу | по окончании загрузки — `server_status connected="true"`; при ошибке — `<server_status connected="error">Ошибка подключения</server_status>` |
| `connect` при активном соединении | `Соединение уже установлено...` | — |
| `disconnect` | `<result success="true"/>` | `server_status connected="false"` **до** возврата |
| `disconnect` без соединения | `Соединение не установлено...` | — |
| `get_connector_version` | `<result success="true"/>` | `<connector_version>…</connector_version>` |
| `subscribe`/`unsubscribe` с `<alltrades>` (`secid` или `security`) | `<result success="true"/>` | `<alltrades>` при поступлении сделок, см. [alltrades.md](alltrades.md) |
| `get_securities` | `<result success="true"/>` | `<securities>` со всем каталогом, см. [dictionaries.md](dictionaries.md) |

Атрибуты `server_status` в порядке DLL: `sys_ver` (SYS# из ответа RQ_PWD), `build` (BLD#), `server_tz`, `id` (SID#), `connected`.

`server_tz` и `sec_tz` инструментов DLL получает из 16-байтового блока часового пояса: из ответа RQ_TIME и из хвоста R_SEC_NEW. Блок похож на `TIME_ZONE_INFORMATION` в 16-битных полях. На демо-сервере встречены два блока, оба сверены с DLL на 17 176 инструментах: нулевой — `UTC`, `4cff 0000 c4ff …` (смещение −180, летнее −60) — `Russian Standard Time`. Для незнакомого блока нативный коннектор оставляет имя пустым.

## Нативная реализация

- `connect` разбирает `commands.Connect` (`host`, `port`, `login`, `password`, `rqdelay`, `request_timeout`) и сразу возвращает успех. Дальше в фоне идут логин, RQ_TIME, RQ_STARTUP, колбэки `<markets>`, `<boards>`, `<candlekinds>` (побайтно как у DLL), выборка снапшота RQ_MAIN с колбэками `<securities>` на каждый опрос с новыми инструментами, `<pits>`, затем `server_status connected="true"`. После этого RQ_MAIN опрашивается каждые `rqdelay` (по умолчанию 100 мс, не меньше 10 мс), как в DLL, и это держит сессию.
- Потеря связи: колбэк `server_status connected="false"`, коннектор готов к новому `connect`. Восстановление (`RQ_RELOGIN`/`RQ_RECOVER`, `recover="true"`) пока не реализовано.
- `disconnect` останавливает опрос, даже если запрос RQ_MAIN уже в полёте. Затем отправляет `RQ_LOGOUT` (без ответа, таймаут 4 с) и отдаёт колбэк `connected="false"` до возврата.
- `subscribe`/`unsubscribe` поддерживают `<alltrades>`; `<quotations>` и `<quotes>` отвечают «not implemented».
- `get_securities` отдаёт `<result success="true"/>` и колбэк `<securities>` со всем каталогом, элемент в элемент как DLL (`FUN_18011f4a0`, см. [dictionaries.md](dictionaries.md)).
- Остальные 36 команд из таблицы DLL отвечают `Command is not implemented by the native connector.`, а если требуют соединения и его нет — сообщением DLL.
- `get_connector_version` возвращает `6.47.2.26.4-native`: версию DLL, поведение которой воспроизводится, с пометкой реализации.
- Колбэки доставляются по порядку из одной горутины, как из потока колбэков DLL.

Расхождения с DLL, известные на сегодня:
- DLL в конце загрузки перед `connected="true"` отдаёт `<client>`, `<positions>`, `<overnight status="false"/>` (живой захват 4.11) — нативный коннектор их не эмитит (торговый контур, M6); события `<sec_info_upd>` (684 за 81 с live-сессии) тоже ещё не эмитятся, см. [dictionaries.md](dictionaries.md).
- Перед каждым `<server_status>` (в конце загрузки и в ответ на команду `server_status`) DLL отдаёт `<overnight status="false"/>` — наблюдение из живого захвата 4.11, генератор не проверяли; нативный коннектор эмитит только статус.
- Тексты ошибок разбора XML и проверки параметров `connect` у DLL не сверялись.

## Проверка

```sh
go test -race ./native/
# сценарий команд на демо-сервере: DLL (под Wine) и Go
harness.exe '<command id="connect">…</command>' sleep:8s '<command id="server_status"/>' …
go run ./tools/reference-harness -backend native '<command id="connect">…</command>' sleep:8s '<command id="server_status"/>' …
```

Тест `native/connector_test.go` поднимает поддельный TRANSAQ-сервер на тех же примитивах протокола (логин, сессионный ключ, RQ_TIME демо-сервера, RQ_STARTUP из фикстуры). Он сверяет весь сценарий из таблицы выше с ответами DLL, а также потерю связи, ошибку подключения и `disconnect` во время опроса.
