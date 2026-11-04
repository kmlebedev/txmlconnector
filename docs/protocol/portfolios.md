# Портфели: портфели Fond/MCT/FORTS и united (Task 22) — D, oracle в работе

Дата: 2026-11-05. Источник: строки/литералы DLL + декомпилятор (RE-1). Статусы: **D** —
декомпилятор, **O** — оракул, **S** — живой сервер, **I** — вывод.

## Команды (D, строки DLL)

- `get_portfolio` — портфель клиента (Fond) — обменник `RQ_FOND_PORTFOLIO` (86);
  строки `RQ_FOND_PORTFOLIO`, `RQ_FOND_PORTFOLIO send failed`,
  `RQ_FOND_PORTFOLIO incorrect server response`.
- `get_portfolio_mct` — MCT-портфель → `<portfolio_mct client="…">` … `</portfolio_mct>`
  (строки `Failed to get portfolio mct`).
- `get_united_portfolio` — объединённый портфель → `<united_portfolio/>`
  (строки `United portfolio calculation failed`).
- `get_mc_portfolio` — MC-портфель → `<mc_portfolio …>` … `</mc_portfolio>`
  (строки `Failed to get mc portfolio`, `Failed to get mc portfolio united (getmarket error)`).
- Портфельные данные могут отдаваться как `<portfolio_tplus/>` (T+ портфель) и
  `<portfolio_currency>` … `</portfolio_currency>` (валютная часть) — литералы DLL.

## Обменники/запросы (D, строки DLL)

- `RQ_FOND_PORTFOLIO` (86) — запрос портфеля Fond; ответ — прямой reply (не RQ_MAIN), парсер
  в том же обменнике; layout запроса/ответа **не разобран (I)** — нужен Ghidra-проход по
  функциям, ссылающимся на строки `RQ_FOND_PORTFOLIO send failed/incorrect server response`.
- `RQ_MARGIN_POS` (84) / `RQ_MARGIN_POS_LIST` (85) — маржинальные позиции; строки
  `MarginPosRequest/MarginPosRequestList return incorrect buffer`, `RQ_MARGIN_POS(_LIST) write failed`;
  layout — I.
- `RQ_GET_RPSREPO` (82) — РПС/РЕПО-портфель: `RQ_GET_RPSREPO write failed`, `read failed`,
  `RQ_GET_RPSREPO Next failed`; `RQ_RPSREPO_SUBSCR` — подписка на изменения РПС/РЕПО;
  `TRN_NEWRPSREPO` — соответствующая транзакция; layout — I.
- `RQ_FORTS_FIRM_MONEY` (47) — деньги FORTS-фирмы (см. `limits.md`: не восстановлено; `<forts_money>`
  форма снята оракулом в `positions.md`).

## Статус и дальнейшие шаги

- Сейчас: D-инвентаризация (эта страница). Oracle-съёмка: dump-редакция S>C кадров reply типов
  86 / 84 / 85 / 82 по схеме RE-3 (как task-24/35) + команды `get_portfolio*`; ожидаемые XML —
  `<portfolios>`, `<portfolio_tplus>`, `<portfolio_forts>`, `<portfolio_mct>`, `<mc_portfolio>`,
  `<united_portfolio>`, `<portfolio_currency>`.
- Живая S-съёмка — после открытия демо (09:50 МСК): команды неторговые, согласие есть.
- Фикстур `portfolio-*` пока нет (честно: oracle/live не сняты).
- I: точные форматы запросов/ответов всех перечисленных RQ_*, состав полей, условия вывода
  XML-элементов; для `RQ_MARGIN_POS*` и `RQ_GET_RPSREPO` неизвестны даже размерности.
## D-форматы обменников (RE-1, preliminary; I)

- **RQ_FOND_PORTFOLIO (86 = 0x56)**, обменник `FUN_180383d30`: ответ читается как `G, G, W, W, W,
  W (+смещения +6, +7, +8, +9), W` — предварительно 2 packed-G + 7 W (точное назначение полей и
  наличие масок — I; строки `RQ_FOND_PORTFOLIO send failed / incorrect server response`).
- **RQ_MARGIN_POS_LIST (85 = 0x55)**, обменник `FUN_180382940`: ответ — 7 × packed-G (I).
- **RQ_MARGIN_POS (84 = 0x54)**, обменник `FUN_180383100`: ответ — ~19 полей в структуру со
  смещениями: `G+8, G+0x10, W+0x18, W+0x40, W+0x1a, G+0x20, G+0x28, G+0x30, G+0x38, W+0x80,
  W+0x82, W+0x84, W+0x86, B+0x88, G+0x48, G+0x50, G+0x58, G+0x60, …` (stride/семантика — I).
- **RQ_GET_RPSREPO (82 = 0x52)**: отправка найдена в функции, ссылающейся на строку `RQ_GET_RPSREPO`
  (в декомпилированной секции `FUN_1803b5c30` вместе с другими обменниками; точный адрес
  обменника требует отдельного прохода — I); ответ/поля не восстановлены.
- **RQ_RPSREPO_SUBSCR** — подписка на изменения РПС/РЕПО (строки send failed); layout — I.

## Oracle/фикстуры

- Oracle-съёмка reply 84/85/86/82 через dump-редакцию + команды `get_portfolio*` — следующий шаг
  (схема RE-3 как task-24/35); фикстур `portfolio-*` пока нет.
- Утренняя S-сессия: те же команды живьём; план — `docs/deepseek/re-session-checklist.md`.EOF
echo ok

## O-находка: portfolio-команды — ЛОКАЛЬНЫЙ рендер (без запросов к серверу)

Прогон всех portfolio-команд под replay (DLL на loopback, tcproxy-захват): в C>S **нет** кадров
86/84/85/82 — команды `get_portfolio`, `get_united_portfolio`, `get_mc_portfolio` отвечают
`<result success="true"/>` и рендерят XML **из уже загруженных сторов** (money/positions из RQ_MAIN
записей `0x3b` + клиентских данных). `get_portfolio_mct` с `client="код"` → «{8} Неверное значение
параметра szClient» (ожидает иной формат/числовой id); `get_mc_portfolio` без client →
«Attributes union and client are empty».

Снятые формы (O, обезличено):

```xml
<portfolio_currency currency="RUB"><cross_rate>1</cross_rate><open_balance>300000.00</open_balance>
<balance>300000.00</balance><equity>300000.00</equity><cover>300000.00</cover><init_req>0.00</init_req>
<maint_req>0.00</maint_req><unrealized_pnl>0.00</unrealized_pnl></portfolio_currency>
```

`<mc_portfolio client="TESTCLIEN">` содержит `<open_equity><equity><pl><go><cover><init_req>
<maint_req><unrealized_pnl>`, вложенные `<portfolio_currency …>` и `<money name="Фондовый рынок МБ"
currency="RUB"><balance_prc><open_balance><bought><sold><balance><blocked><estimated><fee><vm><finres>
<cover>`, а также `<value_part register="T0">…` (micex_registers-опция). Полный образец —
фикстура `portfolio-mc-dll.xml`.

**Фикстуры:** `portfolio-currency-dll.xml`, `portfolio-mc-dll.xml` (O-формы, обезличены);
input-фикстуры (reply/hex) пока не применимы: commands local render — для unit-теста нужен вход из
инъекции `0x3b`-записей money/секций + клиентских записей (`0x02`), что и станет парой
`portfolio-*-inject.hex` + `*-dll.xml` (следующий шаг, вместе с построением стора).
- I: как именно считаются equity/cover/init_req/maint_req/unrealized_pnl (формулы), поведение
  `get_portfolio_mct` (формат client), `<portfolio_tplus>`/`<portfolio_forts>`/`<united_portfolio>`
  (в этом прогоне не отдались; в первом probe были `<portfolio_tplus>`×2 и `<united_portfolio>`).EOF
echo ok

## Статус (итог ночной RE-сессии)

- **local render (S/O):** команды get_portfolio / get_united_portfolio / get_mc_portfolio
  отвечают success и рендерят XML локально из сторов (в C>S нет кадров 86/84/85/82) — подтверждено
  оракулом на записанной сессии; <portfolio_currency> и <mc_portfolio> формы сняты и лежат в
  фикстурах portfolio-currency-dll.xml, portfolio-mc-dll.xml (обезличены).
- **D:** имена/строки обменников и preliminary-форматы 86/84/85/82 (FUN_180383d30/FUN_180382940/
  FUN_180383100; RQ_GET_RPSREPO — адрес требует уточнения).
- **I (не восстановлено):** формулы equity/cover/init_req/maint_req/unrealized_pnl; формат параметра
  client для get_portfolio_mct (сейчас «{8} Неверное значение szClient»); <portfolio_tplus>,
  <portfolio_forts>, <united_portfolio>; RQ_FORTS_FIRM_MONEY (47) layout.
- **Утро (S):** команды неторговые — портфели снимаются в S-серии после тиков/лимитов по
  docs/deepseek/re-session-checklist.md; input-фикстуры RE не снимает (go-engineer синтезирует
  0x02/0x3b через testserver и сверяет с нашими XML-фикстурами).
