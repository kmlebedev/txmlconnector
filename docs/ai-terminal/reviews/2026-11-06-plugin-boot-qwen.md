# Ревью Qwen 3.8 Flash: исправление загрузки плагина в DSH

Дифф: `dsh-ai-terminal/index.js` (optional inject, путь workflow), `dsh-ai-terminal/cordis.patch.yml`
(удалена MCP-client строка), `dsh-ai-terminal/test/optional-services.test.mjs`.
Контекст: фатальный старт DSH «web boot: 1 entry did not activate @local/dsh-ai-terminal: loading».
Вердикт Qwen: **OK** (с P2/P3-замечаниями) → триаж Lead.

| # | Замечание | Серьёзность | Решение |
|---|---|---|---|
| 1 | `pluginDir()` должен резолвиться от `import.meta.url`, иначе тест может ложно пройти | P2 | **Проверено**: `pluginDir()` определён в `index.js` через `fileURLToPath(import.meta.url)` (+`realpathSync`), возвращает каталог пакета — замечание снято |
| 2 | `effects` не диспозятся в тесте | P3 | Принято как допустимое для юнит-теста (процесс завершается); в `dispose`-путях проверено отдельными тестами |
| 3 | Тест зависит от наличия `workflows/nightly-research.js` | P3 | Файл — часть пакета (`git ls-files` подтверждает), ENOENT перехвачен и не ломает assertions |
| 4 | Нет проверки, что env-сеттеры `DSH_AI_TERMINAL_MCP_*` удалены | P3 | Env-сеттеры удалены; тест проверяет отсутствие warning'ов и активацию без optional-сервисов |

Существенных (P1) замечаний нет; функционал tool bridge/LLM bridge/skills/панели сохранён, MCP `/mcp`
на бэкенде остаётся доступным внешним клиентам.