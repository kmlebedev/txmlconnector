---
name: trading-pattern-author
description: Use when the agent drafts, reviews or promotes a trading-pattern SKILL from walk-forward, order-book replay, volatility-oracle or execution-fingerprint evidence — it fixes the point-in-time evidence block, writes SKILL.md, and promotes the pattern only after the out-of-sample evidence policy passes. Paper-only; never live.
whenToUse: Use when a research run produced a walk-forward result, an order-book replay, a volatility oracle forecast or execution fingerprints and the agent wants to turn it into a reusable trading pattern — or when a human asks to review or promote an existing pattern draft.
---

# Торговый паттерн: черновик и promotion по доказательствам

Навык описывает, как превратить результат исследования терминала в паттерн-SKILL и когда его
можно продвинуть. Паттерн — это черновик с out-of-sample доказательствами, а не торговая
рекомендация. Каталог по умолчанию — `<dataDir>/skills` вне git; в репозиторий паттерн попадает
только через PR человека.

## 1. Когда использовать

- агент закончил walk-forward (`research.WalkForwardResult`), LOB-replay (`lobreplay.Result`),
  прогноз оракула волатильности (`volatility.Forecast`) или отпечатки исполнения
  (`fingerprint.Footprint`) и хочет зафиксировать найденный паттерн;
- человек просит «оформи паттерн», «покажи доказательства», «продвинь черновик»;
- НЕ использовать для live-решений: `validated` не включает live-исполнение.

## 2. Доказательства (Evidence)

Каждый паттерн обязан нести `Evidence`: `DataFrom`/`DataTo` (окно данных), `AsOf` (момент
решения), `Source` (`lob|bars`), `OOS backtest.Metrics`, `DeflatedSharpe`, `PBO`, `Trials`,
`OOSTrades`, `DataHash`.

- `bars`: `skills.EvidenceFromWalkForward(walkForwardResult, asOf, dataHash)` — OOS-метрики
  сшитых окон, `Trials` — размер сетки параметров.
- `lob`: `skills.EvidenceFromLOB(lobreplayResult, asOf, dataHash)` — один replay даёт OOS-метрики,
  но `DeflatedSharpe`/`PBO`/`Trials` равны нулю: для promotion нужен walk-forward OOS (`bars`).
- `DataHash` — отпечаток датасета (винтажный срез). Без отпечатка паттерн не продвигается.
- Контекст рынка — `skills.MarketContext(forecast, footprints)`: режим волатильности и отпечатки
  исполнения. Это контекст гипотезы, а не доказательство.

## 3. Формат SKILL.md

Файл `<dir>/<id>/SKILL.md`, где `<id>` — lowercase-hyphen slug (`[a-z0-9-]`, 1..64).
Frontmatter (обязательно):

- `name` (равен id), `description` (английское, начинается с `Use when`), `whenToUse`;
- `metadata.status`: `draft | validated | retired`;
- `metadata.evidence`: `dataFrom`, `dataTo`, `asOf`, `source`, `trials`, `oosTrades`,
  `deflatedSharpe`, `pbo`, `dataHash`.

Тело: «Гипотеза», «Инструменты», «Условия», «Вход», «Выход», «Риск», «Доказательства
(out-of-sample)», «Ограничения», «Статус». Структуру собирает `skills.WriteDraft`, вручную
файл не пишется.

## 4. Promotion policy (значения по умолчанию)

- `MinOOSTrades = 30`, `MinDeflatedSharpe = 0.95`, `MaxPBO = 0.3`, `MinOOSMonths = 3`.
- `skills.Promote` проверяет: OOS-окно существует (`From < To`, не меньше 3 месяцев), OOS-сделок
  не меньше 30, deflated Sharpe не ниже 0.95, PBO не выше 0.3, есть `DataHash`, `Source` из
  `lob|bars`. Чего не хватает — вернуть списком, статус остаётся `draft`.
- `validated` лишь делает навык видимым агенту; live это не включает.

## 5. Инструменты агента (фаза F)

- `draft_skill_pattern` — принимает доказательства и формулировку, генерирует id и пишет черновик
  через `skills.WriteDraft` в `<dataDir>/skills`; возвращает путь и сводку.
- `list_skill_patterns` — каталог черновиков и validated-паттернов (`skills.List`).
- Продвижение в репозиторий делает человек через PR; агент не коммитит паттерны.

## 6. Чего не делать (red lines)

- не обещать доходность и не называть паттерн доказанным без out-of-sample;
- не предлагать live-торговлю и реальные заявки; `validated` — только видимость навыка;
- не писать в SKILL логины, пароли, счета, номера клиентов и любые секреты;
- не подгонять OOS: выбор стратегии по in-sample, повторные прогоны без поправки на число
  испытаний (`Trials`) и `DeflatedSharpe`;
- не публиковать паттерн в git — только PR человека.

## 7. Ограничения

- Паттерны исследовательские и paper-only: прошлые результаты не гарантируют будущих.
- LOB-паттерны без walk-forward OOS остаются `draft`.
- Метрики моделей — вывод (**I**): их считает движок терминала; живые прогоны только по согласию.
- Каталог паттернов лежит вне git; в репозитории живёт только сам навык `trading-pattern-author`.


## 8. Работа вне DSH: установка через npx и MCP

Навык переносим: DSH-провайдер плагина читает этот же файл из \`skills/\` (один источник, без
копий), а другие агенты ставят его командой:

\`\`\`
npx skills add github.com/kmlebedev/txmlconnector --skill trading-pattern-author
\`\`\`

Вне DSH данные для Evidence берутся теми же REST-инструментами, что и в DSH: MCP \`/mcp\` с
заголовком \`X-Terminal-Token\` или \`trading-terminal/scripts/terminal.sh\` (\`vol\`, \`news\`,
\`backtest\`, \`history\`). Токен (\`TERMINAL_API_TOKEN\`) и креды TRANSAQ (\`TC_LOGIN\`,
\`TC_PASSWORD\`) живут только в \`.env\`, никогда — в чате, промпте или SKILL.md.

Черновики и validated-паттерны лежат вне git в \`<dataDir>/skills\`: в репозиторий паттерн попадает
только через PR человека. Реальные заявки недоступны: validated не включает live.

