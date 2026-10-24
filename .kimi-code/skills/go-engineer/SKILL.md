---
name: go-engineer
description: Use when the task is Go code in txmlconnector — the Win32 DLL adapter behind connector.Connector, the gRPC server/broker, client/commands, proto, Docker/Wine build, tests or CI — and you need the repo-specific implementation and review cycle
type: prompt
whenToUse: When working on connector lifecycle, native_windows_amd64 syscalls, server/service/broker, client stream/reconnect/queue, proto regeneration, Windows cross-build, Docker/Wine or tests in txmlconnector
---

# Go-инженер в txmlconnector

**Назначение.** Маршрут Go-задачи от постановки до проверенного изменения: что прочитать, какие инварианты сохранить, как реализовать и что считается готовым результатом. Скил — надстройка над README и docs, а не их замена.

**Канонические документы.** [README.md](../../../README.md); [docs/architecture.md](../../../docs/architecture.md); [docs/ROADMAP-GO.md](../../../docs/ROADMAP-GO.md); [docs/TRANSAQ-SERVER-RESEARCH.md](../../../docs/TRANSAQ-SERVER-RESEARCH.md); [proto/connect.proto](../../../proto/connect.proto); [Makefile](../../../Makefile); [CI](../../../.github/workflows/go-test.yml).

## 1. Когда применять

- `connector.Connector`, Win32-адаптер, загрузка DLL, callback, память DLL.
- `server`: lifecycle, `service`, gRPC-контракт, `broker`, graceful shutdown.
- `client`: stream, `response_queue`, `reconnect`, `client/commands`.
- `proto/connect.proto` и перегенерация; совместимость gRPC-контракта.
- Makefile, CI, Docker/Wine-сборка, `examples/*`.
- Тесты и документация для всего перечисленного.

**Когда не применять:** восстановление сетевого протокола TRANSAQ, Ghidra, разбор DLL и криптографии (это M1–M3 из [ROADMAP-GO.md](../../../docs/ROADMAP-GO.md), материал — [TRANSAQ-SERVER-RESEARCH.md](../../../docs/TRANSAQ-SERVER-RESEARCH.md)); замена вендорской DLL; бизнес-логика внешнего экспортёра, который только потребляет XML.

## 2. Что прочитать перед стартом

1. [README.md](../../../README.md) и [docs/architecture.md](../../../docs/architecture.md): компоненты, границы, lifecycle, failure policy.
2. [docs/ROADMAP-GO.md](../../../docs/ROADMAP-GO.md): текущий этап и границы. M1–M3 — исследование; M4+ — Go-реализация. Текущий DLL-адаптер не считается нативным backend M4.
3. Затрагиваемый пакет и его тесты: `connector/`, `server/`, `client/`, `client/commands/`.
4. При изменении контракта — [proto/connect.proto](../../../proto/connect.proto), сгенерированные `connect.pb.go` / `connect_grpc.pb.go`, потребители в `server` и `client`.
5. [Makefile](../../../Makefile) и [.github/workflows/go-test.yml](../../../.github/workflows/go-test.yml): чем именно проверяется результат.
6. [TRANSAQ-SERVER-RESEARCH.md](../../../docs/TRANSAQ-SERVER-RESEARCH.md) — только если задача касается поведения протокола; не превращай гипотезу в утверждение.

## 3. Границы роли

| Go-инженер делает | Не делает |
|---|---|
| `connector`, Win32 API/stdcall, DLL lifecycle | Не правит вендорскую DLL и не коммитит её изменения |
| `server`, `service`, `broker`, gRPC-контракт | Не меняет номера полей proto без отдельной задачи |
| `client`, `commands`, reconnect, очередь ответов | Не редактирует `*.pb.go` вручную |
| Тесты, Makefile, CI, Docker/Wine-сборка, examples | Не добавляет CGO и не обещает нативный Linux-запуск DLL |
| Документация по затронутому контракту | Не выдаёт непроверенное на Windows/Wine за приёмку |

## 4. Цикл работы

1. **Зафиксируй scope.** Один логический change set: issue, пункт роадмапа или явно сформулированная задача. Если issue старый — сначала воспроизведи проблему.
2. **Прочитай минимум** по §2; не читай весь research-документ ради одной правки.
3. **Изолируйся.** Ветка от `main`; при параллельных сессиях — отдельный worktree. Не смешивай рефакторинг с поведенческим изменением.
4. **Сначала тест.** Для поведения — тест, который падает без правки. Используй фейковый `connector.Connector` и фейковый gRPC-stream; реальные DLL, Wine и сеть в тестах не нужны.
5. **Реализуй по слоям.** Сначала инвариант и ошибки, затем happy path; не расширяй API «на будущее».
6. **Проверь.** `gofmt`, `go vet`, `go test -race ./...`, `go build ./...`, `make server_build`, `make queues_build` (см. §8).
7. **Ревью.** Пройди §11: границы платформ, конкурентность, владение памятью, proto, тесты, секреты.
8. **Зафиксируй.** Commit/PR: что изменено, зачем, какие команды прогнаны и что именно проверено. Если Windows/Wine-прогона не было — напиши «не проверено».
9. **Стоп-правила.** Объём вырос → отдельная задача. Приёмка не выполнена → не называй работу готовой. Документация противоречит коду → зафиксируй расхождение и реши его в рамках задачи.

## 5. Чек-лист изменения

**Connector и Win32-адаптер:**

- [ ] `New` не делает внешней работы; `Start` загружает DLL, находит процедуры, вызывает `Initialize` и `SetCallback`; `Close` идемпотентен и вызывает `UnInitialize` один раз.
- [ ] Windows-код — только `connector/native_windows_amd64.go` с `//go:build windows && amd64`; для остальных платформ — `native_unsupported.go` с явной ошибкой.
- [ ] Все вызовы DLL сериализованы; контекст проверяется до и после захвата мьютекса.
- [ ] Callback: скопировать XML, освободить память DLL до возврата, `recover` от паники, не вызывать lifecycle/команды, не блокироваться.
- [ ] Память ответа освобождается; ошибка `FreeMemory` в `SendCommand` проверяется.
- [ ] Ошибки оборачиваются через `%w`; библиотека не вызывает `log.Fatal`.

**Server, service, broker:**

- [ ] `server.Serve` принимает listener и `connector.Connector`; Win32-импортов в `server` нет.
- [ ] `SendCommand` валидирует пустой запрос и маппит ошибки в `InvalidArgument` / `Internal` / `context`-коды.
- [ ] `broker.publish` не блокируется; медленный подписчик отключается и получает `ResourceExhausted`, остальные продолжают работу.
- [ ] Закрытие подписки идёт через `done`/`closed`, а не через закрытие канала, в который пишет `publish`.
- [ ] Graceful shutdown ограничен таймаутом; connector закрывается ровно один раз.

**Client:**

- [ ] Stream readiness: сервер сначала отправляет Header, клиент не должен потерять начальный snapshot. Текущий `NewTCClientWithConn` запускает ожидание Header, но `Connect` вызывает сразу; при правке этого места добавь тест на порядок и приведи код к документации.
- [ ] `responseQueue` сохраняет FIFO и не блокирует `Recv`; при изменении помни, что очередь неограничена и не должна стать утечкой памяти.
- [ ] Типизированные каналы буферизованы; отмена освобождает dispatch и не оставляет горутин.
- [ ] Переподключением управляет один `RunWithReconnect`; не появляется второй конкурирующий цикл.
- [ ] XML-разбор проверяет ошибки; неизвестное сообщение не роняет stream.

**Proto:**

- [ ] Источник истины — `proto/connect.proto`; после правки — `make compile`, сгенерированные файлы в том же change set.
- [ ] Номера полей и wire-контракт не меняются без необходимости; новые поля — с новыми номерами.
- [ ] Обновлены `server` и `client`; сгенерированный пакет — `transaqConnector`, в коде импортируется как `pb`; `go_package` не переименовывается без миграции.

**Build, examples, docs:**

- [ ] `CGO_ENABLED=0`; сервер собирается под `windows/amd64`.
- [ ] `make server_build` и `make queues_build` проходят; Docker — `make build`, если менялся Dockerfile/контекст.
- [ ] `make server` / `make client` не используются как проверка сборки: они запускают процессы.
- [ ] Новый код не делает сетевых вызовов в `init()`; `examples/queues` с RabbitMQ в `init()` — legacy, не тиражировать.
- [ ] README/architecture/roadmap обновлены, если изменился контракт; завершённый пункт «Ближайших задач» отмечен в том же PR.

## 6. Особые случаи

- **Новый platform backend.** Отдельный файл в `connector` с build tag; `native_unsupported.go` остаётся явной ошибкой для неподдержанных платформ; `server`, `client` и proto не меняются, если контракт тот же.
- **Новый RPC или сообщение.** Сначала proto и тест совместимости, затем service и client. Не добавляй второй способ передать тот же XML.
- **Legacy-шимы.** `server.TxmlSendCommand`, `server.Messages`, `server.Done` существуют только на `windows/amd64` и помечены deprecated. Сохраняй их поведение, но новый код пиши через `connector.Connector`; не добавляй новые зависимости от шимов.
- **Версия DLL.** `TC_DLL_PATH` важнее `TC_DLL_VER`; в репозитории лежит `txmlconnector64-6.47.2.26.4.dll`, а default в коде — `6.43.2.24.0`. Для стенда задавай путь явно, не выводи версию из имени без проверки.
- **Callback и паника.** Паника не должна пересекать границу с foreign code; возвращай неуспех в DLL, а не роняй процесс. Не вызывай из callback `SendCommand`, `Close`, `Start`.
- **Медленный подписчик.** Не увеличивай буфер «чтобы не терять»: сначала определи, допустима ли потеря; текущая политика — отключить отстающего и вернуть `ResourceExhausted`.
- **Reconnect.** Один владелец цикла; если меняешь client reconnect, проверь, что server/service не создаёт второй цикл восстановления.
- **Docker/Wine.** Wine — runtime-хост для `server.exe`, не способ загрузить DLL в Linux-процесс. Docker-образ должен собирать `windows/amd64` бинарник с `CGO_ENABLED=0`.
- **Секреты.** `TC_LOGIN`, `TC_PASSWORD`, `TC_HOST`, `TC_PORT` — только из окружения. Не логируй пароль и сырые XML с данными счёта; не добавляй credentials в README, compose и примеры.
- **Нет Windows/Wine.** Это не повод считать задачу проверенной: напиши, что именно осталось непроверенным, и какие команды выполнены.

## 7. Go-правила репозитория

1. **Платформенная граница.** Win32 API и stdcall-вызовы DLL — только в `connector`; `server`, `client`, `proto`, `examples` не импортируют `golang.org/x/sys/windows` и не делают прямых DLL-вызовов.
2. **Один владелец DLL.** Никаких параллельных вызовов; мьютекс на lifecycle и `SendCommand`; контекст проверяется до и после ожидания.
3. **Callback — hot path.** Копирование, `FreeMemory`, `recover`, короткий handler; тяжёлую работу — в broker, без блокирующих отправок.
4. **Память DLL.** Не сохраняй указатели после возврата; копируй строку; освобождай ответ и callback-указатель.
5. **Lifecycle.** `New` не загружает DLL; `Start` делает внешнюю работу и возвращает ошибки; `Close` идемпотентен; в библиотеке нет `log.Fatal`.
6. **gRPC-граница.** Валидируй `nil`/пустые сообщения; используй `codes.InvalidArgument`, `Internal`, `ResourceExhausted`; не допускай паники в handler.
7. **Broker.** `publish` не блокируется; подписка закрывается через `done`; не закрывай канал сообщений, пока в него может писать publisher.
8. **Client.** Не теряй readiness handshake; сохраняй FIFO; буферизуй каналы; отмена должна освобождать `Recv` и dispatch; reconnect — один.
9. **Proto.** Правь `.proto`, затем `make compile`; не редактируй generated-файлы; не меняй номера полей.
10. **Конфиг и секреты.** Только `os.Getenv`; без сетевых вызовов в `init()` нового кода; не печатай credentials и чувствительные XML.
11. **Тесты.** Фейковый connector, `bufconn`, фейковый stream, `-race`; проверяй ошибки, отмену, медленного подписчика и порядок сообщений.
12. **Сборка.** Pure Go, `CGO_ENABLED=0`; сервер — `windows/amd64`; проверяй `make server_build` и `make queues_build`, а не только `go test` на macOS.

## 8. Команды

| Цель | Команда | Что проверяет |
|---|---|---|
| Форматирование | `gofmt -l .` | пустой вывод |
| Vet | `go vet ./...` или `make vet` | отсутствие диагностик |
| Тесты | `go test ./...` или `make test` | пакеты на текущей платформе |
| Race | `go test -race ./...` | гонки в тестах и конкурентных путях |
| Сборка всех пакетов | `go build ./...` | компиляция под текущую платформу |
| Windows-сервер | `make server_build` | `bin/server.exe`, `CGO_ENABLED=0` |
| Windows-очереди | `make queues_build` | `bin/queues.exe` |
| Proto | `make compile` | перегенерация `connect.pb.go` / `connect_grpc.pb.go` |
| Docker | `make build` | образ `kmlebedev/txmlconnector:local` |
| CI | `go test -v -race -coverprofile=coverage.txt ./...` + `go vet ./...` | ровно то, что делает workflow |
| Запуск сервера | `make server` | runtime под Wine, не CI |
| Запуск клиента | `make client` | собирает и запускает клиент; нужен сервер |

`make server` и `make client` запускают процессы. Для проверки сборки используй `make server_build`, `make queues_build` и `go build ./...`.

`make compile` требует `protoc` и Go-плагины. Если их нет — не правь generated-файлы вручную, зафиксируй «не проверено». CI работает на Ubuntu: Windows-файлы там не исполняются, поэтому `make server_build` и `make queues_build` обязательны локально.

## 8.1. GoLand MCP

- Проверь, есть ли `mcp__jetbrains__*`. Если есть — `get_file_problems`/`lint_files` для правок, `get_symbol_info` для сигнатур, `search_symbol`/`analyze_calls` для навигации, `rename_refactoring` для переименований.
- Передавай `projectPath` = абсолютный путь рабочего дерева; результаты фильтруй по своему пути, если IDE объединяет несколько проектов.
- Не используй терминал IDE, run-конфигурации и `build_project`: проверки — командами из §8.
- Если инструментов нет, работай `Grep`/`Read`/`Edit` и скажи об этом одной строкой.

## 9. Definition of Done

- [ ] Scope назван; diff не выходит за задачу, нет «попутных» рефакторингов.
- [ ] Тест добавлен или обновлён; `go test -race ./...` зелёный.
- [ ] `gofmt -l .` пуст; `go vet ./...` и `go build ./...` проходят.
- [ ] `make server_build` и `make queues_build` проходят; Windows-код компилируется.
- [ ] Для не-Windows платформ `native_unsupported.go` возвращает явную ошибку, а не ломает сборку.
- [ ] Проверены конкурентность, владение памятью, callback и broker.
- [ ] Proto перегенерирован, если менялся; номера полей не сдвинуты.
- [ ] README/architecture/roadmap синхронны; скил обновлён, если изменился описанный контракт.
- [ ] Нет секретов, CGO, правок DLL и ручных правок generated-файлов.
- [ ] В PR есть команды и результат; непроверенное на Windows/Wine явно помечено.

## 10. Частые ошибки

| Ошибка | Почему плохо | Как правильно |
|---|---|---|
| Параллельные вызовы DLL | Vendor не потокобезопасен: race/crash | Мьютекс на `SendCommand` и lifecycle |
| Тяжёлая работа или команда в callback | Deadlock вендорской библиотеки | Копировать, освободить, быстро вернуть; работа — в broker |
| Забыт `FreeMemory` | Утечка памяти DLL | Освобождать ответ и callback-указатель |
| Сохранён указатель DLL | Use-after-free | Сначала скопировать строку, потом освободить |
| Паника в callback | Пересекает foreign code и роняет процесс | `recover` и неуспешный результат в DLL |
| Блокирующая отправка подписчику | Останавливает callback и всех клиентов | Non-blocking `publish`, отключение медленного |
| Ручная правка `*.pb.go` | Затрётся при `make compile` | Править `.proto` и перегенерировать |
| Смена номеров полей proto | Ломает wire-совместимость | Новые номера для новых полей |
| `Connect` без readiness | Можно потерять начальный snapshot | Ждать Header по документации и тестировать порядок |
| Несколько reconnect-циклов | Дубли сессий и гонки | Один `RunWithReconnect` |
| `make client` в CI | Собирает и запускает процесс | `go build ./...` |
| Только `go test ./...` на macOS | Windows-файлы не компилируются | `make server_build` / `make queues_build` |
| Windows-импорты вне `connector` | Ломают сборку на Linux/macOS | Держать их в файле с build tag |
| Логирование `TC_PASSWORD` или XML счёта | Утечка секретов | Только env, без чувствительных полей |
| Сетевой `init()` в новом коде | Побочные эффекты при импорте и в тестах | Делать в `Start`/`Run` |
| «Проверено» без Windows/Wine | Ложная приёмка | Писать «не проверено» и перечислять пробелы |

## 11. Ревью-чек-лист

- [ ] Diff соответствует задаче; нет новых зависимостей и API без необходимости.
- [ ] Build tags и платформенная граница соблюдены; stub для не-Windows на месте.
- [ ] DLL-вызовы сериализованы; callback не блокируется, память освобождается, паника не проходит наружу.
- [ ] `broker` не блокирует publisher; медленный подписчик отключается; закрытие не приводит к send on closed channel.
- [ ] Client сохраняет порядок, корректно отменяется, не теряет readiness и не запускает второй reconnect.
- [ ] Proto изменён через `.proto` + `make compile`; номера полей стабильны.
- [ ] Ошибки обёрнуты и не проглочены; gRPC-коды корректны.
- [ ] Тесты покрывают ошибки, отмену, медленного подписчика и порядок; `-race` зелёный.
- [ ] Секреты не попали в код, логи, README и compose.
- [ ] Документация и роадмап синхронны; evidence в PR есть.

## 12. Ссылки

- [README.md](../../../README.md), [docs/architecture.md](../../../docs/architecture.md), [docs/ROADMAP-GO.md](../../../docs/ROADMAP-GO.md).
- [docs/TRANSAQ-SERVER-RESEARCH.md](../../../docs/TRANSAQ-SERVER-RESEARCH.md), [docs/research/inspect_binary.py](../../../docs/research/inspect_binary.py).
- [connector/connector.go](../../../connector/connector.go), [connector/native_windows_amd64.go](../../../connector/native_windows_amd64.go), [connector/native_unsupported.go](../../../connector/native_unsupported.go).
- [server/server.go](../../../server/server.go), [server/service.go](../../../server/service.go), [server/broker.go](../../../server/broker.go).
- [client/main.go](../../../client/main.go), [client/reconnect.go](../../../client/reconnect.go), [client/response_queue.go](../../../client/response_queue.go).
- [proto/connect.proto](../../../proto/connect.proto), [Makefile](../../../Makefile), [.github/workflows/go-test.yml](../../../.github/workflows/go-test.yml).
