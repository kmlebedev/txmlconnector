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