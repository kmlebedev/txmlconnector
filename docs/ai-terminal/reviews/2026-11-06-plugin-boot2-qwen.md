# Ревью Qwen 3.8 Flash: минимальный inject и дочерний `ctx.inject(['tools'])`

Дифф: `dsh-ai-terminal/index.js` (inject = ["webServer"], tools монтируется дочерним ctx.inject) и
`test/optional-services.test.mjs`. Контекст: повторный фатальный web-boot DSH («1 entry did not
activate @local/dsh-ai-terminal: loading») после того, как в обязательный inject добавили `tools`.
Вердикт Qwen: **CHANGES** → триаж Lead.

| # | Замечание | Серьёзность | Решение |
|---|---|---|---|
| 1 | `backend.onRunning` перезаписывается в `mountTools`, возможна утечка/гонка замыканий | P2 | **Отклонено с проверкой**: `backend.onRunning/onStopped` назначаются один раз в начале `apply` и читают изменяемую ссылку `toolBridge`; `mountTools` их не переписывает, `release` обнуляет `toolBridge` и закрывает bridge, поэтому повторный вызов безопасен (идемпотентен) |
| 2 | `invokeTool` теперь всегда функция (rejection) вместо `null` | P3 | **Принято как осознанный контракт**: автономия вызывает его только при наличии workflowEngine; ясная ошибка `инструменты Harness недоступны` лучше скрытого null-пути; потребители — только `autonomy.js`, ошибка обрабатывается |
| 3 | Тест child-inject не проверяет сценарий «tools так и не появился» | P3 | **Исправлено**: добавлен тест `apply survives a deferred tools service` (колбэк ctx.inject сохраняется, плагин активируется, позже колбэк с пустым ctx не падает); `node --test` — 53/53 |

Существенных (P1) замечаний нет; после правки P3 тестовый набор покрывает оба сценария: `tools`
появился и `tools` не появился.