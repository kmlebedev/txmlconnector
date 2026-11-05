# Переносимые торговые навыки (npx skills add)

Каталог `skills/` — единственный источник ручных навыков AI-терминала. Один и тот же `SKILL.md`
подхватывает и DSH-плагин `dsh-ai-terminal` (провайдер навыков), и любой агент, установивший навык
через `npx skills add`: Cursor, Claude Code, Qwen Code, Codex. Копий нет: правится один файл.

## Установка

```bash
# из репозитория GitHub
npx skills add github.com/kmlebedev/txmlconnector --skill trading-terminal
npx skills add github.com/kmlebedev/txmlconnector --skill trading-pattern-author
npx skills add github.com/kmlebedev/txmlconnector --skill trading-automation-author
```

Ручная проверка установки в чистом Claude Code/Cursor (Step 5 задачи 25) **не выполнялась** —
статус **I**: автоматически проверяются только frontmatter, исполняемость скрипта и README
(`go test ./skills`) плюс smoke `trading-terminal/scripts/terminal.sh` против заглушки бэкенда.
Локальная установка перед публикацией:

```bash
npx skills add /path/to/txmlconnector --skill trading-terminal
```

## Навыки каталога

| Навык | Когда использовать | Что внутри |
|---|---|---|
| `trading-terminal` | котировка/свечи/стакан/бэктест/новости/прогноз волатильности из любого агента | формат `rules.Spec`, инструменты бэктеста, стоимость и размер выборки, closed-bar правило, REST-скрипт и MCP |
| `trading-pattern-author` | оформить найденный паттерн из walk-forward/LOB/оракула с доказательствами | Evidence (OOS, deflated Sharpe, PBO, data hash), promotion policy, red lines |
| `trading-automation-author` | повторяющаяся последовательность инструментов агента должна стать автоматизацией (Go-функция, MCP-инструмент, модель, расписание) | ledger токенов/hotspots, mini-plan задачи (Files/Interfaces/тест), бэклог `docs/ai-terminal/automation-backlog.md` |

## Настройка окружения

REST-скрипт `trading-terminal/scripts/terminal.sh` и MCP-сервер читают одни и те же переменные из
`.env` рядом с проектом (или `~/.config/ai-terminal/.env`):

```bash
TERMINAL_URL=http://127.0.0.1:8090
TERMINAL_API_TOKEN=local-api-token
TC_LOGIN=transaq-login
TC_PASSWORD=transaq-password
```

`.env` не коммитится; значения никогда не печатаются в чат, логи, промпты и SKILL.md. Скрипт
читает только перечисленные ключи и не экспортирует остальное окружение.

## Два способа работы

1. **MCP.** Если терминал запущен, внешние MCP-клиенты получают каталог инструментов на
   `POST /mcp` (JSON-RPC 2.0, заголовок `X-Terminal-Token`), см. `docs/ai-terminal/ARCHITECTURE.md`
   §10 и `docs/ai-terminal/dsh-integration.md`.
2. **REST-скрипт.** `trading-terminal/scripts/terminal.sh` — тонкая curl-обёртка над тем же API,
   только чтение, без торговых операций:

```bash
sh scripts/terminal.sh health
sh scripts/terminal.sh instruments
sh scripts/terminal.sh quote TQBR:SBER
sh scripts/terminal.sh book TQBR:SBER
sh scripts/terminal.sh history TQBR:SBER 300 700
sh scripts/terminal.sh strategies
sh scripts/terminal.sh backtest TQBR:SBER rsi_reversion 300
sh scripts/terminal.sh news 20
sh scripts/terminal.sh vol TQBR:SBER
```

## Правила безопасности

- Токен терминала и креды TRANSAQ — только в `.env`; никогда в чате, промпте, SKILL.md или коммите.
- Реальные заявки недоступны: терминал paper-only, `validated` не включает live-исполнение.
- Перед выводами проверяй размер выборки (не меньше 30 сделок для выводов) и стоимость
  (комиссия/проскальзывание в бэктесте).
- Не подгоняй параметры под тестовый отрезок: для паттерна обязательны out-of-sample доказательства
  и `DeflatedSharpe`/`PBO`.

## DeepSeek Harness

Плагин `dsh-ai-terminal` регистрирует провайдер навыков из этого каталога: в DSH навык приходит от
провайдера, без копирования файлов. Сгенерированные агентом паттерны читаются из `<dataDir>/skills`
и показываются только в статусе `validated` (черновики не видны агенту до promotion).

## Ограничения

- Навыки исследовательские и paper-only: без гарантий доходности.
- REST-скрипт требует запущенного бэкенда и доступен только на loopback; MCP — так же.
- Реальная маршрутизация заявок в итерации 1 не реализована.

## Проверка

```bash
go test ./skills -v
```

`TestSkillsFrontmatterValid` проверяет frontmatter каждого `skills/*/SKILL.md` (name = имя
каталога, description < 1024 символов), отсутствие token-like строк, существование и executable-бит
упомянутых скриптов, наличие REST-скрипта `trading-terminal/scripts/terminal.sh` и строки
`npx skills add` в этом README.

