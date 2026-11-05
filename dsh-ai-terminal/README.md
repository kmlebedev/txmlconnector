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

## Dev-проверка Host-половины

```bash
make terminal
node --test dsh-ai-terminal/test/           # юнит-тесты Host-половины, без Go-бэкенда
node dsh-ai-terminal/test/proxy-smoke.mjs   # smoke с реальным Go-бэкендом (порты 18097/18098)
```
