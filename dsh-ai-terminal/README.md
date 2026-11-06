# AI Terminal — DSH plugin

Встраивает нативный AI-терминал (MOEX/TRANSAQ, `cmd/terminal`) в интерфейс
DeepSeek Harness как **основную панель** с иконкой в левой панели Harness.

![AI Terminal](../docs/ai-terminal/screenshot-main.png)

## Что делает

- добавляет в sidebar Harness иконку **AI-терминал**; клик открывает main panel
  с полноценным терминалом (график, стакан, портфель, заявки, AI-агент,
  автономные стратегии);
- Host-половина плагина запускает Go-бэкенд терминала (по умолчанию
  `bin/terminal` этого checkout'а, при отсутствии — собирает `go build`),
  ждёт `/api/health` и **проксирует** SPA и API под тем же origin, что и
  Harness: `/ai-terminal/` (REST + SSE, без CORS и второго порта в браузере);
- генерирует на каждый запуск UUID-токен API и nonce `instanceId`, передаёт их бэкенду в env
  (`TERMINAL_API_TOKEN`, `TERMINAL_INSTANCE_ID`) и подставляет `X-Terminal-Token` в проксируемые
  запросы; бэкенд на порту принимается только при совпадении `instanceId` — чужой процесс не
  переиспользуется (см. «Ограничения и безопасность»);
- панель в стиле Harness (тема `--dsw-alias-*`, локали en/ru/zh) показывает
  статус бэкенда, кнопки «Обновить», «Перезапустить», «Открыть в браузере»;
- при выгрузке плагина/остановке Harness дочерний бэкенд завершается.

## Требования

- профиль DSH с Web UI (плагин использует Client slots `main` и
  `sidebar.panellist`);
- Go 1.27+ (если `bin/terminal` отсутствует и его нужно собрать) или заранее
  собранный `bin/terminal` (`make terminal`).

## Установка

Плагин ставится как локальный bundle:

```bash
# из корня checkout'а txmlconnector
make terminal                      # bin/terminal
# затем в DSH-сессии: plugin_manager install_bundle с target = <repo>/dsh-ai-terminal
```

Или вручную в профиль (не рекомендуется — используйте plugin_manager):

```yaml
- insert:
    - id: ai-terminal
      name: '@local/dsh-ai-terminal'
      config: { mode: offline, port: 18090, autoStart: true }
```

## Конфигурация (row `ai-terminal`)

| Ключ | По умолчанию | Смысл |
|---|---|---|
| `mode` | `offline` | `offline` — встроенный снапшот демо-данных; `live` — живой read-only TRANSAQ (нужны env `TRANSAQ_*`/`TC_*` в окружении запуска Harness; плагин передаёт их бэкенду явно, см. «Ограничения и безопасность»); `auto` |
| `port` | `18090` | loopback-порт Go-бэкенда; наружу не публикуется |
| `autoStart` | `true` | запускать бэкенд при загрузке плагина |
| `repoDir` | авто | checkout с `cmd/terminal` (по умолчанию — родитель каталога плагина) |
| `binaryPath` | авто | заранее собранный бэкенд; по умолчанию `<repoDir>/bin/terminal` |
| `llmProvider` | `deepseek-official` | provider Harness LLM для агента терминала (мост задачи 10) |
| `llmModel` | `deepseek-flash` | модель Harness LLM для агента терминала; `request.model` бэкенда игнорируется |
| `dataDir` | — | каталог сгенерированных паттернов; его `skills/` отдаёт провайдер навыков |
| `autonomy` | `{enabled:false, tokenBudget:200000, maxWallMinutes:60, approvalMode:'confirm'}` | автономный ночной workflow, бюджет токенов и kill-switch (см. «Автономный ночной цикл») |

### Автономный ночной цикл (`autonomy`, план Task 21)

Host-половина поднимает workflow **`ai-terminal-nightly`** и расписание Harness:

- **Расписание.** Задача «AI-терминал: ночное исследование», cron `30 0 * * 2-6`
  (`Europe/Moscow` — 00:30 МСК, ночи после понедельник–пятница), target `new_once`.
  Prompt расписания просит агента новой сессии вызвать инструмент
  `terminal_nightly_research` и пересказать сводку. При `autonomy.enabled: false` задача
  не создаётся, а ранее созданная — ставится на паузу (идемпотентно по title).
- **Workflow.** `dsh-ai-terminal/workflows/nightly-research.js` передаётся в
  `ctx.workflowEngine.start({script, meta})` как текст JS-body; фазы: данные → волатильность
  (asOf=now) → walk-forward по универсуму → роботы (`detect_execution_algos`) → черновики
  навыков (`draft_skill_pattern`) → `propose_automation` → итоговая сводка. Мутирующие действия
  не выполняются: предложения ждут подтверждения человека (`approvalMode: 'confirm'`).
- **Инструмент.** `terminal_nightly_research` (`autonomy.js`) запускает workflow, блокирует
  вызов до завершения и возвращает сводку в сессию; повторный запуск при активном прогоне
  отклоняется.
- **Бюджет.** Пока прогон активен, плагин перехватывает waterfall `llm/stream` и суммирует
  `usage` чанки детей; при превышении `tokenBudget` — `run.cancel('token-budget')`.
  `maxWallMinutes` отменяет прогон с причиной `max-wall`.
- **Kill-switch.** Вкладка правой панели **«Автономия»** (последний прогон, бюджет, кнопки
  «Запустить сейчас» / «Стоп»); control API плагина: `GET /ai-terminal-ctl/autonomy/status`,
  `POST /ai-terminal-ctl/autonomy/run` (202, не блокирует), `POST /ai-terminal-ctl/autonomy/stop`
  (отменяет workflow и вызывает бэкендовый `stop_autonomy`).

### Пульт бота (`relay`, план Task 26)

Host-реле связывает роботов терминала с DSH-сессией, в которой их создали:

- **Подписка.** SSE бэкенда `/api/events` (с `X-Terminal-Token`, напрямую к loopback) — событие
  `{"type":"bot","data":{"botId","entry"}}`. При обрыве/перезапуске бэкенда реле переподключается и
  догоняет журнал каждого бота через `GET /api/bots/{id}/journal?after=<lastSeq>`, дедуп по
  `entry.seq` — ни потерь, ни дублей.
- **Кому.** Значимые события уходят агенту сессии, создавшей бота: маппинг `botId → sessionId`
  берётся из вызовов `terminal_bot_*` (`exec.agent.id` — это id сессии в реестре агентов Harness;
  при отсутствии живого агента — fallback `ctx.sessionController.prompt` с тем же содержимым).
- **Что.** `signal`, `order`, `fill`, `risk_check`-отказ, `reconnect`, `error`, `stage`;
  `closed_bar` без сигнала в агента **не** идёт — только в карточку. Лимит
  `relay.maxAgentEventsPerHour` (по умолчанию 12) на сессию в час; избыток копится и уходит одной
  сводкой при `relay.summaryBatch` (по умолчанию 5) событиях.
- **Клиентские поверхности.** Карточки `terminal_bot_*` (`tool.call.toolview`); у карточки
  бэктеста — кнопки «Проверить» / «Dry-run» / «На бумагу» (та же REST: `POST /api/bots`,
  `/{id}/check`, `/{id}/start`, `/{id}/promote` → proposal «Подтвердить/Отклонить»); живая
  карточка бота в `conversation.chat.turnTail` (стадия, последняя закрытая свеча МСК, индикаторы,
  сигнал, мини-график по 100 барам, журнал с фильтром по `kind`); мини-версия в
  `conversation.input.dock`; kill-switch «Стоп всех ботов» в `conversation.session.header.actions`
  (виден, только пока есть бот не в `stopped`); вкладка правой панели «Боты».
- **Ограничение сборки.** Клиентский bundle плагина — один `client.js` (Harness client-modules
  раздаёт только пакетный `./client`), поэтому `bot-ui.js` — Host-side view-model (карточки
  `tools.js`, комментарии реле, тесты), а Client рендерит те же структуры в `client.js`.
- **Проверено:** `node --test dsh-ai-terminal/test/` — 39 тестов, включая `test/bot-relay.test.mjs`
  (доставка signal агенту, closed_bar не тратит токены, батчинг сверх лимита, реконнект с afterSeq
  без дублей, kill-switch). **НЕ ПРОВЕРЕНО (Step 5):** живой сценарий из 7 шагов в DSH-сессии,
  реальные SSE/агент-комментарии, скриншоты — требуют живого Harness и LLM; скриншоты не создавались.

**Расхождения с планом Task 21 (зафиксированы, план не переписывался):**

- план: `timing:{kind:'cron', expr:'30 0 * * 2-6'}` → факт: селектор cron
  `{kind:'cron', cron:{expression, time_zone}}` (S: dsh-schedule/README.md:64-70);
- план: `Asia/Moscow` → `Europe/Moscow` (IANA; `Asia/Moscow` не существует);
- бюджет токенов: план «по usage из моста LLM» → факт: workflow-дети ходят через общий
  `ctx.llm`, поэтому usage собирается перехватом `llm/stream` (S:
  dsh-llm/lib/index.js:2403), а не OpenAI-мостом задачи 10;
- расписание доставляет prompt в сессию, а не вызывает плагинный код напрямую, поэтому запуск
  детерминирован инструментом `terminal_nightly_research`;
- `storage` из плана — это сервис `storageDomain` в Harness (S: dsh-integration §2.8).

## Ограничения и безопасность

- Бэкенд слушает только `127.0.0.1`; доступ к панели — в рамках
  аутентифицированной сессии Harness. Отдельного порта в браузере нет:
  всё идёт через `/ai-terminal/` origin Harness.
- **Guard локального API.** Бэкенд защищён middleware `Guard` (Host — только loopback,
  Origin для небезопасных методов, `Content-Type: application/json` для запросов с телом) и
  одноразовым токеном: плагин генерирует `crypto.randomUUID()` на запуск, передаёт его бэкенду
  через env `TERMINAL_API_TOKEN` и сам подставляет в проксируемые запросы заголовок
  `X-Terminal-Token`. Прямой доступ к бэкенду получает токен в cookie `terminal_token`
  (`HttpOnly; SameSite=Strict; Path=/`) при отдаче `index.html`. Подробно —
  [ARCHITECTURE §10.4](../docs/ai-terminal/ARCHITECTURE.md#104-безопасность).
- **Прокси не выпускает креды Harness наружу.** Из проксируемых запросов вычищаются `cookie`,
  `authorization` и все `x-dsh-*`; `Set-Cookie` бэкенда не отдаётся браузеру, чтобы
  token-cookie не утекала в cookie jar Harness. Исходный `Host` сохраняется, поэтому проверка
  Origin в бэкенде остаётся осмысленной.
- **Чужой процесс на порту.** Плагин принимает уже запущенный бэкенд только если
  `/api/health.instanceId` совпадает с `TERMINAL_INSTANCE_ID`, переданным своему ребёнку; иначе
  панель показывает `phase=error` «порт занят чужим процессом», флаг `reused` не выставляется,
  чужой процесс не перезапускается.
- **Порт занят молчащим процессом** (не отвечает на `/api/health`): плагин не может отличить его
  от свободного порта и запускает свой бэкенд. Ребёнок падает с bind error
  (`address already in use`), панель показывает `phase=error` с текстом stderr. Автоперезапусков
  нет, ожидание ограничено `HEALTH_TIMEOUT_MS` (90 с).
- **Env-переменные.** DSH subprocess запускает детей со «scrubbed» родительским env (имена с
  `KEY`/`PASSWORD`/`SECRET`/`TOKEN` вычищаются), поэтому плагин явно передаёт бэкенду токен, nonce
  и терминальные переменные (`TRANSAQ_*`, `TC_*`, `TERMINAL_LLM_*`, `TERMINAL_LIVE_TRADING`) —
  и ничего остального из окружения Harness.
- Реальные ордера в терминале выключены (paper-only; live-режим read-only).
- После изменения `terminal/web/**` пересоберите `bin/terminal`
  (`make terminal`) — Go embed вшивает SPA в бинарь.
- Если в конфиге `live`, а env-креды не заданы, бэкенд завершится; статус
  панели покажет причину, кнопка «Перезапустить» перезапустит его после
  исправления окружения.

## Проверено (2026-11-05/06, профиль `desktop`)

- Плагин установлен через `plugin_manager install_bundle` (`application: applied`,
  без restart); Host-половина подняла бэкенд на `127.0.0.1:18090`
  (`/api/health` → `mode=offline`, 11 бумаг).
- Через origin Harness (`:52729`) проверены: `GET /ai-terminal/` (SPA),
  `/ai-terminal/app.js`, `/ai-terminal/styles.css`, `/ai-terminal/api/health`,
  `/api/instruments`, `POST /api/orders` (paper fill), `POST /api/backtest`
  (700 баров), `POST /api/agent/chat` (локальный аналитик), SSE
  `/ai-terminal/api/events` (поток `quote`), `POST /ai-terminal-ctl/restart`
  (бэкенд перезапущен, health восстановился).
- Терминал, загруженный через плагин, проверен в реальном браузере (CDP):
  11 инструментов, `ДЕМО/онлайн`, график и SSE-котировки —
  [скриншот](../docs/ai-terminal/screenshot-dsh-embedded.png).
- Client-половина: слоты `sidebar.panellist` и `main` содержат активную запись
  `ai-terminal` (проверено `cordis_inspect_query`); установлены locale en/ru/zh
  и иконка.
- **Guard локального API (2026-11-06, задача 1):** `go test -race ./terminal/api
  ./cmd/terminal`, `make verify`, `node --test dsh-ai-terminal/test/` (2 теста: strip
  credentials + токен; отказ от чужого `instanceId`) — зелёные;
  `node dsh-ai-terminal/test/proxy-smoke.mjs` с реальным Go-бэкендом — `phase=running`,
  REST/SSE/restart 200; ручная проверка бинаря: 401 без токена, 403 на чужой Origin, 415 на
  `text/plain`, 421 на чужой Host, cookie с `HttpOnly/SameSite=Strict`.
- **Не проверено:** визуальный вид самой обёртки панели внутри Harness
  (аутентифицированную страницу нельзя скриншотить без управления браузером).
  Проверка: открыть Harness → иконка «AI-терминал» в левой панели → панель
  должна показать статус и встроенный терминал.

- **Автономный ночной цикл (2026-11-06, задача 21):** `node --test dsh-ai-terminal/test/` —
  все тесты Host-половины зелёные, включая `test/autonomy.test.mjs`: остановка по бюджету
  токенов (fake `ctx.on('llm/stream')`), отсутствие расписания при `enabled:false`, пауза
  существующего расписания, kill-switch (cancel + `stop_autonomy`), идемпотентное создание
  cron-расписания и регистрация `terminal_nightly_research`.
- **Не проверено (задача 21, Step 5):** живой прогон workflow в Harness на офлайн-данных —
  сводка в сессии, черновики в `<dataDir>/skills`, задачи в `automation-backlog.md`, реальный
  `ctx.schedule`/LLM-водопад и живой kill-switch. Скриншоты не создавались; требуется живой
  DSH с Web-композицией и настроенным LLM.

### Контракт nightly и проверка результата

Workflow использует единый `asOf`, передаёт ответы исследований фазе паттернов и
явно запрашивает запись automation backlog. Поле `verification: required` означает,
что завершение фаз не доказывает успешность инструментов или создание файлов.
Проверяйте ответы и реальные артефакты; отсутствие данных — ограничение, не успех.
Настройте `dataDir` вне git: этот путь передаётся бэкенду как `TERMINAL_DATA_DIR`;
при пустом значении nightly не должен обходить ошибку каталога собственным `dir`.
Для PIT-исследований учитываются только закрытые (`open + period <= asOf`) не-`Partial` бары.
Подробности и границы проверки: [аудит T21](../docs/ai-terminal/t21-nightly-contract-audit.md).

## Dev-проверка Host-половины

```bash
make terminal
node --test dsh-ai-terminal/test/           # юнит-тесты Host-половины, без Go-бэкенда
node dsh-ai-terminal/test/proxy-smoke.mjs   # smoke с реальным Go-бэкендом (порты 18097/18098)
```
