# Ревью Qwen 3.8 Flash: упаковка плагина (files[]) и live-acceptance

Дифф: `dsh-ai-terminal/package.json` (+ автономия/бот-модули/`workflows/*.js` в `files[]`) и новый
`dsh-ai-terminal/test/packaging.test.mjs`. Вердикт Qwen: **OK**.

| # | Замечание | Серьёзность | Решение |
|---|---|---|---|
| 1 | `runtimeModules()` сканирует только корень и плоский `workflows/` — будущие вложенные каталоги не будут пойманы | P3 | Принято как известное ограничение: расширим при появлении вложенных runtime-каталогов; текущее дерево покрыто |
| 2 | glob-семантика `files[]` и логика `covered()` | OK | — |
| 3 | тесты ловят пропущенные модули и битые exports | OK | — |

Контекст: задачи T21/T26 добавили `autonomy.js`, `bot-relay.js`, `bot-ui.js`,
`workflows/nightly-research.js`, но не обновили `files[]` — опубликованный пакет плагина не содержал бы
эти модули (импорт `index.js` упал бы). Исправлено; тест `test/packaging.test.mjs` защищает от регрессии.