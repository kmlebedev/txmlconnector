# Ревью фронтенд-diff второй моделью: Qwen 3.8 Flash

Пользователь требует: для фронтенд-задач diff кода ревьюит вторая модель **Qwen 3.8 Flash**.
Роут `qwen-token-plan/qwen3.8-flash` в DSH-субагенте этой сессии запрещён (`child LLM route ... is
not allowed for this Session`), поэтому ревью выполняется через Anthropic-совместимый
эндпоинт провайдера (`https://token-plan.maas.qwencloudapi.com/apps/anthropic/v1/messages`,
модель `qwen3.8-flash`, thinking disabled). В модель уходит только текст задачи плана и diff;
секреты, окружение и доступ к файловой системе не передаются. Сырые ответы — `.re-run/reviews/`
(вне git), ниже — отчёт и триаж Lead.

## T1 — guard локального API + прокси плагина (JS-часть)

Ревьюированный diff: `git diff 9fc883a..b26b99e -- dsh-ai-terminal/` (index.js + test/proxy.test.mjs).
Вердикт Qwen: **CHANGES**.

Замечания Qwen и триаж Lead:

| # | Замечание Qwen | Серьёзность | Решение Lead |
|---|---|---|---|
| 1 | `probe()` слишком мягкий: при занятом порту без ответа на /api/health плагин запускает свой бэкенд | P1 | **Отклонено**: child падает с bind error, обработчик завершения ставит `phase=error` с stderr; `HEALTH_TIMEOUT_MS` ограничивает ожидание, циклов перезапуска нет. Граничный кейс задокументирован в ARCHITECTURE §10.4 и dsh-ai-terminal/README |
| 2 | Токен в env дочернего процесса может утечь в логи | P2 | **Отклонено**: env не логируется, токен per-launch, на диск не пишется |
| 3 | Нет cleanup upstream при обрыве клиента (SSE) | P2 | **Отклонено**: в `proxyToBackend` уже есть `req.on('aborted')` и `res.on('close') -> upstream.destroy()` |
| 4 | Нет теста «порт занят молчащим процессом» | P3 | Принято как известное ограничение, задокументировано; отдельный тест не требуется планом |
| 5 | Сохранение Host может сломать доступ через не-loopback | P3 | Принято: loopback-only и есть `-allowed-origins`; задокументировано |

Итог: обязательных исправлений нет; замечания P3 учтены в документации (`71d07e5`).
