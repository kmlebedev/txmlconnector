# PR: AI-нативный терминал в DeepSeek Harness (план 2026-11-05, фазы A–G)

> **PR создан: [#62](https://github.com/kmlebedev/txmlconnector/pull/62)** (draft, base `main`, head `plan/ai-terminal-a`).
> CI «Run Go Tests» — **pass** (2m10s). После живых Step 5 (по согласию пользователя) перевести в ready for review / merge.

**Base:** `main` (9fc883a) · **Head:** `plan/ai-terminal-a` · **Коммитов:** 47 · **Файлов:** 156

## Что и зачем

Терминал из PR #60 становится частью DeepSeek Harness: инструменты терминала — нативные инструменты
Harness, LLM — общий (`ctx.llm.stream`), навыки — один источник для DSH и `npx skills add`, бэктест →
walk-forward → паттерны → автоматизация → бот в одном окне. Весь исполняемый код — Go (stdlib, без
новых зависимостей) + Cordis-плагин; ClickHouse — HTTP, MCP — собственный JSON-RPC.

План: `docs/superpowers/plans/2026-11-05-ai-native-terminal.md` (108/116 шагов исполнено, 8 live/manual
оставлены до живого прогона). Статус и коммиты: `docs/ai-terminal/PROGRESS.md`.

## Фазы (все 26 задач плана закрыты)

| Фаза | Задачи | Ключевые коммиты |
|---|---|---|
| A. Ревью PR #60 | T1–T5 | `b26b99e`, `3051c3e`, `75994d0`, `61c2db0`, `6cfd80a` |
| B. Ядро агента | T6–T8 | `d334f6e`, `23cab2c`, `582df62` |
| C. Терминал в DSH | T9–T10 | `62c5794`, `683cfb7` |
| D. Данные | T11–T14 | `b996425`, `3b066d8`, `5466c8f`, `8393410` |
| E. Исследования | T15–T18 | `3241002`, `e8be80d`, `a0e7586`, `ecfabfd` |
| F. Самообучение | T19–T21 | `ffa0bf7`, `0f16134`, `29e60fa` (+`6248d6b`) |
| G. Бэктест → бот | T22–T26 | `99e6d3f`, `7cc187f`, `f5aad81`, `e55fded`, `adedeac`, `ebeaddb` |
| Интеграция/доки | — | `9572cc1`, `029a155`, `9d7d730`, `3d114fc`, `84b13bf` |

## Финальная приёмка (на HEAD ветки и в CI)

| Проверка | Результат |
|---|---|
| CI GitHub Actions «Run Go Tests» на PR #62 | **pass (2m10s)** |
| `make verify` (gofmt, vet, build, кросс-сборки `native/...`, `go test -race ./...`) | **exit 0** |
| `node --test dsh-ai-terminal/test/` | **39/39 pass** |
| `make terminal_smoke` (offline e2e: guard, instruments, backtest, 32 инструмента, invoke, боты, MCP, SSE, SIGTERM) | **37/37 PASS, exit 0** |
| `make server_build`, `make queues_build` | exit 0 |
| Секреты/фикстуры (verifier): реальных кредов нет, фикстуры обезличены, DLL/go.mod/go.sum не менялись; `native/` изменён только задачей T5 | OK |
| Ревью: Go-diff — Lead; фронтенд-diff — Qwen 3.8 Flash (T1, T6, T7, T8, T9, T10, T16, T24, T26); отчёты и триаж — `docs/ai-terminal/reviews/` | P1/P2 после триажа — нет |

## Не проверено (I, требует живого окружения/согласия)

- Live Step 5: демо-сервер TRANSAQ (T11/T12/T22), ClickHouse-витрины vintage, живой DSH-прогон
  плагина/инструментов/панелей/MCP (T9/T10/T21/T26), ручной браузерный UI, `npx skills add` в чистом
  Claude Code/Cursor, скриншоты. Step 5 соответствующих задач оставлены незакрытыми в плане.
- Phase X (ALFRED-винтажи, календарь FOMC, схема `transaq_quotes`) — отдельные репозитории, вне этого PR.
- Реальная маршрутизация заявок выключена; торговля — только бумажная/replay.

## Отклонение от буквы плана

План предполагал отдельный PR на каждую фазу; фазы исполнялись параллельно (это разрешено планом:
«Параллельно можно вести C, D и G23–G24»), поэтому коммиты фаз перемешаны по времени, и выбран один
интеграционный PR. Разбиение на 7 PR потребовало бы пересборки истории/cherry-pick.