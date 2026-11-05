# Работа команды DeepSeek Harness по плану нативного TRANSAQ-клиента

Каталог `docs/deepseek/` — служебные документы для режима RTC (Lead + teammates). Основные правила —
в корневом [AGENTS.md](../../AGENTS.md); здесь — операционные рецепты: как запустить команду, какие
промпты давать агентам, как проводить ревью и что делать при блокерах.

## Что подготовлено

| Файл | Назначение |
|---|---|
| [AGENTS.md](../../AGENTS.md) | обязательные правила для всех агентов: роли, доска задач, write-scope, red lines, DoD, команды |
| [task-index.md](task-index.md) | все 30 задач плана: фазы, зависимости, write-scope, RE-шаги, тесты, commit, общие файлы, параллельные группы, противоречия |
| [environment.md](environment.md) | фактическое состояние окружения и что из-за этого доступно/заблокировано |
| [tools/re/check_env.sh](../../tools/re/check_env.sh) | быстрая перепроверка окружения (PASS/WARN/FAIL, без секретов) |
| [../superpowers/plans/2026-11-04-native-transaq-client.md](../superpowers/plans/2026-11-04-native-transaq-client.md) | сам план, единственный источник задач |

## Запуск команды: последовательность Lead

1. **Подготовка дерева.** `git status` чистый; `git switch main && git pull`; Lead создаёт
   интеграционную ветку `plan/native-client` (или отдельные ветки на фазу). Пользователь решает,
   работать прямо в рабочем дереве или в worktree.
2. **Проверка окружения.** `tools/re/check_env.sh`; результаты сверить с
   [environment.md](environment.md). Wine установлен (11.21) — RE-2/RE-3 доступны; для них нужны
   `TC_PASSWORD` в `~/re/demo.env` и/или записанная сессия, иначе шаг остаётся `blocked`.
3. **Декомпозиция.** Взять из [task-index.md](task-index.md) ближайшую параллельную группу. На каждую
   задачу — карточка `team_task_create` с `write_scopes` (пути из плана) и `blocked_by` (номера
   карточек, а не задач плана).
4. **Найм.** `spawn_teammate` на каждую роль: 1–2 go-engineer (disjoint write-scope), при нужде
   RE-engineer и verifier. В промпт — номер карточки, задача плана, ссылки на AGENTS.md и скилл,
   границы. Lead сам не пишет код в файлах, которые пишет teammate.
5. **Контроль.** `team_task_list` и `wait_agent`; конфликт write-scope решается до старта, а не во
   время merge. Промежуточные отчёты — только через `send_message` на `lead`.
6. **Проверки и ревью.** На итоговом дереве: `make verify` + критерии задачи (RE-4 и т.п.). Затем
   ревью второй моделью (Qwen 3.8 Flash, см. §«Ревью»). P1/P2 — исправить до закрытия карточки.
7. **Сдача.** Lead сводит коммиты, обновляет `docs/protocol/*`, `docs/ROADMAP-GO.md`, готовит PR по
   фазе (по команде пользователя). Финальный ответ пользователю — с командами, результатами и явным
   перечнем непроверенного.

## Шаблоны промптов

**Kickoff фазы (пользователь → Lead):**

```
Работаем по AGENTS.md и docs/deepseek/task-index.md. Фаза <N> плана
docs/superpowers/plans/2026-11-04-native-transaq-client.md. Разбей задачи <номера> на карточки с
write-scope и blocked_by, найми исполнителей и доведи до зелёного make verify. Живые торговые шаги и
демо-сервер — только после моего согласия. В конце — отчёт: карточки, коммиты, проверки, что не проверено.
```

**Исполнитель (go-engineer):**

```
Ты go-engineer команды DeepSeek Harness в /Users/whitefox/GolandProjects/txmlconnector.
Прочитай AGENTS.md и .kimi-code/skills/go-engineer/SKILL.md целиком. Твоя карточка: <task-id>, задача
<N> плана docs/superpowers/plans/2026-11-04-native-transaq-client.md (прочитай её целиком, включая
Review Focus и Global Constraints). Работай по шагам плана: падающий тест → реализация → проверки §7
AGENTS.md → commit с сообщением из плана. Не выходи за write-scope: <пути>. Не трогай общие файлы:
<список>. Отчёт — Lead: что сделано, команды и результаты, что не проверено, блокеры.
```

**Реверс-инженер (RE-engineer):**

```
Ты RE-engineer команды DeepSeek Harness в /Users/whitefox/GolandProjects/txmlconnector.
Прочитай AGENTS.md, tools/re/README.md и .claude/skills/reverse-engineering/SKILL.md. Твоя карточка:
<task-id>, задача <N> плана. Выполни RE-шаги из задачи: <RE-1/RE-2/RE-3>. Каждое утверждение — с
координатой (адрес функции, байты payload, строка лога) и статусом S/O/D/I в docs/protocol/<файл>.md.
Фикстуры — в native/records/testdata/<record>-{live,inject}.hex, обезличить (TESTCLIENT/TESTUNION/
TESTLOGIN), длины сохранить. Живые торговые команды и смену пароля не отправлять без явного согласия
пользователя в чате. Если Wine/демо-сервер недоступны — доложить, а не подменять оракул догадкой.
```

**Ревьюер (Qwen 3.8 Flash / fallback):**

```
Ты независимый ревьюер. Проект txmlconnector, план docs/superpowers/plans/2026-11-04-native-transaq-client.md,
задача <N>. Проверь diff <base>..<head> и файлы: <список>. Критерии: соответствие шагам плана и
Interfaces; типы (model.Decimal, time.Time UTC, никакого float64 для денег); отсутствие секретов и
лишних данных в фикстурах; гонки и конкурентный доступ; обработка ошибок, ErrOutcomeUnknown без
повторной отправки RQ_TRANSACT; полнота тестов из плана (включая Review Focus). Верни только:
P1/P2/P3-замечания с файл:строка и вердикт OK/CHANGES. Код не правь.
```

**Verifier:**

```
Ты verifier. Не меняя код, прогони на текущем дереве AGENTS.md §9: gofmt -l ., go vet ./...,
go build ./..., кросс-сборки CGO_ENABLED=0 ./native/..., go test -race ./..., make verify. Сверь
write-scope с карточками и фактические файлы с планом. Отчёт: какая команда, exit code, ключевые
строки, расхождения. Ничего не исправляй.
```

## Ревью второй моделью

- Модель: **Qwen 3.8 Flash**, provider `qwen-token-plan`, model `qwen3.8-flash` (1M контекст).
  Вызов — через инструмент `workflow` с override модели; без доступа к записи файлов.
- **Проверка доступности перед волной задач** (petit probe; `null`/ошибка = модель недоступна):

  ```js
  const r = await agent("Ответь ровно: QWEN_OK",
    { provider: "qwen-token-plan", model: "qwen3.8-flash", label: "qwen-probe" });
  return r;
  ```

- **Известная проблема (2026-11-04).** В профиле DSH baseURL провайдера —
  `https://token-plan.maas.qwencloudapi.com/apps/anthropic` без `/v1`, поэтому адаптер получает
  `404 InvalidParameter: Not support` (проверено: прямой запрос на `…/anthropic/v1/messages`
  даёт 200, на `…/anthropic/messages` — тот же 404). Исправление — добавить `/v1` к baseURL в
  `~/.dsh/profiles/desktop/cordis.patch.yml` (правка вне репозитория, только с согласия пользователя).
- **Fallback до исправления:** `provider: "joingonka"`, `model: "zai-org/GLM-5.3-Flash"` (проверено
  рабочим) или отдельный субагент на `deepseek-flash`. В отчёте писать, какой моделью ревьюили.
- Ревью не заменяет проверки: сначала `make verify`, потом модель. P1/P2 — блокеры закрытия карточки.

## Сборка и запуск сервера (Task 29 / RE-4)

- `make server_build` — Windows-адаптер `bin/server.exe` (vendor DLL); `make server` по умолчанию
  запускает его под Wine (прежнее поведение).
- `TC_BACKEND=native make server` — pure-Go backend **без Wine и без Windows-сборки**: цель делает
  `go run .`, слушает `TC_LISTEN_ADDR` (по умолчанию `:50051`). На macOS/Linux это основной
  способ поднять gRPC-сервер без DLL.
- `TC_BACKEND=dll make server` — явно Win32 DLL под Wine (нужен Windows amd64/Wine).
- RE-4 сценарий без Wine: терминал 1 — `TC_BACKEND=native make server`; терминал 2 — клиент
  `TC_TARGET=localhost:50051 TC_RE4_SEQUENCE=1 go run ./examples/grpc-client` (connect выполняет сам клиент, затем get_securities -> subscribe -> disconnect);
  дальнейшие команды (`get_securities`, `subscribe`, `disconnect`) отправляются gRPC-клиентом
  из UI/скрипта. Живой демо-сервер — только с согласия пользователя (утренняя S-серия).

## Worktree-режим (DSH) и GoLand MCP

Проверено 2026-11-04 в этом рабочем пространстве.

### Worktree

- Плагин `@deepseek-ai/dsh-experimental-tool-worktree` включён, инструмент — `create_worktree`.
  Он создаёт **одну** ветку+checkout за вызов и меняет cwd сессии; автоматического «worktree на каждый
  шаг» нет — Lead (или исполнитель) вызывает его на задачу.
- По умолчанию checkout: `<корень checkout>/.agents/worktrees/<branch>` (проверено: вернулся
  `/Users/whitefox/GolandProjects/txmlconnector/.agents/worktrees/<name>`, `baseCommit` = HEAD).
- Чтобы получить привычный `.worktrees`, нужен патч профиля DSH `~/.dsh/profiles/desktop/cordis.patch.yml`
  (вне репозитория, с согласия пользователя, затем перезапуск DSH):

  ```yaml
  - id: worktree
    name: '@deepseek-ai/dsh-experimental-worktree'
    config:
      directory: .worktrees
  ```

  Оба пула (`.agents/`, `.worktrees/`) добавлены в `.gitignore`.
- Вернуться в корень: `working_directory({"cd":"/Users/whitefox/GolandProjects/txmlconnector"})`.
  Следующий worktree создавать из корня, не изнутри worktree (иначе пул вложится в worktree).
- Worktree от `HEAD` не содержит незакоммиченных `AGENTS.md`/`docs/deepseek/*`, и DSH снимает
  инструкции в таком worktree (проверено). Перед worktree-режимом подготовку нужно закоммитить.

### GoLand MCP (`mcp__jetbrains__*`)

- В текущем профиле DSH MCP-клиент **не настроен** → инструментов нет. Патч:

  ```yaml
  - id: mcp-jetbrains
    name: '@deepseek-ai/dsh-mcp-client'
    config:
      serverName: jetbrains
      transport: streamable-http
      url: http://127.0.0.1:64422/stream
  ```

- Сервер GoLand MCP **отвечает** (`GoLand MCP Server 2026.2.3`, протокол 2024-11-05). В этой версии
  GoLand отдаёт **один** tool `execute_tool` (`command` = `get_file_problems --filePath …`,
  `projectPath` = путь проекта); полный список под-инструментов — `execute_tool` с командой `help`.
- **Ограничение (проверено):** сервер на `64422` привязан к проекту GoLand
  `clickhouse-import-rosstat`; `projectPath` не переключает проект — `get_file_problems`/`lint_files`
  отвечают `points to the location outside of the project directory` даже для `native/client.go`.
  `read_file` и `search_symbol` работают по абсолютным путям, но инспекции — нет. Чтобы Go-файлы
  `txmlconnector` и его worktree были доступны, открой проект (или worktree) в том окне GoLand, к
  которому привязан MCP-порт, либо запусти отдельный инстанс GoLand для этого проекта и укажи его
  MCP-порт в патче. После этого `projectPath` = корень проекта/worktree, а файлы worktree лежат внутри
  него.

### Секреты: `~/re/demo.env`

`TC_LOGIN`/`TC_PASSWORD` и Wine-экспорты живут **вне репозитория** — в `~/re/demo.env`
(`chmod 600`); шаблон версионируется как [`tools/re/demo.env.example`](../../tools/re/demo.env.example):

```bash
cp tools/re/demo.env.example ~/re/demo.env
chmod 600 ~/re/demo.env
$EDITOR ~/re/demo.env                 # заполнить TC_LOGIN и TC_PASSWORD
set -a; . ~/re/demo.env; set +a       # перед живыми командами (RE-2/RE-3/RE-4)
```

Заполненный `*.env` в git не попадает (`.gitignore`); значения не печатать, не логировать, не
копировать в фикстуры. `tools/re/check_env.sh` проверяет только наличие (`set`/`unset`) и факт
`~/re/demo.env`; заполнил ли её пользователь — видно по `TC_PASSWORD: set`.

## Доска задач: соглашения

- `subject` — «Task N: <название из плана>».
- `write_scopes` — репо-относительные пути из секции Files задачи (директория/файл), плюс тесты.
  Совпадение scopes у двух активных карточек — сигнал сериализовать задачи.
- `blocked_by` — карточки-зависимости; ставить по Interfaces/Consumes плана и по общим файлам.
- `claim` только с текущей ревизией; `complete` — после зелёных проверок, фикстур, документации и
  отчёта Lead. Карточка без отчёта не закрывается.
- Статусы смотреть `team_task_list`; Lead перепроверяет `ready` перед наймом.

## Блокеры и что с ними делать

| Блокер | Признак | Решение |
|---|---|---|
| Нет `TC_PASSWORD` | `tools/re/check_env.sh` → WARN `TC_PASSWORD: unset` | **Снято 2026-11-04:** `~/re/demo.env` заполнен, секреты доступны, согласие пользователя на живые операции получено; загружать `set -a; . ~/re/demo.env; set +a` |
| Wine | `tools/re/check_env.sh` → `PASS Wine: wine-11.21` | установлен (`~/re/wine`, `~/re/wine-prefix`), `run_dll.sh` подставляет пути и DLL; проверено `get_connector_version` |
| Демо-сервер закрыт | 23:50–09:50 МСК, выходные | живые шаги отложить; в отчёте — «окно демо-сервера» |
| Qwen 404 | ревью-субагент вернул `null` | fallback GLM-5.3-Flash / deepseek-flash; Lead сообщает пользователю про `/v1` |
| Worktree не в `.worktrees` | `create_worktree` вернул `.agents/worktrees/...` | патч `worktree.directory: .worktrees` **уже добавлен** 2026-11-04; действует после перезапуска DSH. До перезапуска — `.agents/worktrees` |
| GoLand MCP не в сессии | нет инструментов `mcp__jetbrains__*` | патч `mcp-jetbrains` **уже добавлен** 2026-11-04; действует после перезапуска DSH |
| MCP: `outside of the project directory` | project-scoped инструмент на `native/client.go` | MCP привязан к другому проекту GoLand; открыть `txmlconnector`/worktree в этом инстансе или использовать его MCP-порт |
| Ghidra под sandbox | `analyzeHeadless` → `Unable to prompt user for JDK path` / `Path element starting with '.' is not permitted` | `mkdir -p "${TMPDIR:-/tmp}/dsh-ghidra-home"`, `JAVA_HOME=openjdk@25`, `JAVA_TOOL_OPTIONS=-Duser.home=…`; проект — `${TMPDIR:-/tmp}/txc-ghproj` (Ghidra 12.2 отвергает '.'-пути), симлинк `.re-run/ghproj`; проверено 2026-11-04 (import succeeded ~3 м 50 с) |
| Конфликт write-scope | две карточки на один файл | сериализовать: `blocked_by` или передать владение; не мержить параллельные правки одного файла |
| План противоречит коду/DLL | невозможно выполнить шаг | зафиксировать в `docs/protocol/` со статусом, сообщить Lead; задачу не «додумывать» |

## Проверка готовности к запуску (быстрый чек-лист)

- [ ] `tools/re/check_env.sh` отработал, статусы сверены с [environment.md](environment.md).
- [ ] `AGENTS.md` прочитан всеми участниками (он же подхватывается DSH как workspace instructions).
- [ ] Скиллы прочитаны: go-engineer у исполнителей, reverse-engineering у RE.
- [ ] Карточки созданы с write-scope и зависимостями; ветка `plan/native-client` существует.
- [ ] Ревью-модель проверена пробным вызовом (Qwen или fallback) — результат зафиксирован.
- [ ] Пользователь дал согласие на всё, что выходит за пределы офлайн-работы (Wine, демо-сервер,
      торговые команды, изменение `~/.dsh`).
