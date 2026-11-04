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
