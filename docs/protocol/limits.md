# Лимиты клиента: get_client_limits / `<clientlimits>` (Task 21)

Дата: 2026-11-04/05. Источник: RE-1 (декомпилятор) + RE-3 оракул (replay-server + DLL, только
loopback). Статусы: **D** — декомпилятор, **O** — оракул, **S** — живой сервер, **I** — вывод.

## Команда (O)

```xml
<command id="get_client_limits" client="<код клиента>"/>
```

`client` — **атрибут** команды (код клиента, напр. TESTCLIEN), не элемент; числовой id не
принимается. Ошибки DLL: без клиента — «Client not specified.»; неверный — «Wrong client
specified». `union` — необязательный атрибут.

## RQ_CLIENT_LIMITS (43): формат ответа (D)

Обменник `FUN_1803b4d50` (шлёт `FUN_180355d50(...,0x2b,"RQ_CLIENT_LIMITS")`), парсер — там же.
Ответ — поток записей до `L client_id == -1`:

```
L client_id
  if (session_flags & 2):  G ×4    ; cbplimit, cbplused, cbplplanned, fob_varmargin
  if (session_flags & 4):  W coverage; L liquidity_c; G ×15; (если сервер > 0x4b) G ×2
```

Порядок 15 G = порядок XML после liquidity_c: profit, money_current, money_reserve,
money_free, options_premium, exchange_fee, forts_varmargin, varmargin, pclmargin, options_vm,
spot_buy_limit, used_spot_buy_limit, collat_current, collat_blocked, collat_free.
Масштабы (D): деньги — /100 (`FUN_180136700(v,2)`), `coverage` — W как есть (scale 1),
`liquidity_c` — /10^5 (`FUN_1801365c0(v,5)`).

## XML `<clientlimits client="…">` (D, строитель `FUN_1800f5ea0`)

Элементы (порядок; каждый выводится только при ненулевом поле, entry stride 0xb8):

1. `<cbplimit>` (G, /100)
2. `<cbplused>` (G, /100; закрывающий тег в DLL с опечаткой `</cpblused>`)
3. `<cbplplanned>` (G, /100)
4. `<fob_varmargin>` (G, /100; в декомпиляторе условие/значение по разным смещениям — I)
5. `<coverage>` (W)
6. `<liquidity_c>` (L, /1e5)
7. `<profit>` (G, /100)
8. `<money_current>` (G, /100)
9. `<money_reserve>` (G, /100)
10. `<money_free>` (G, /100)
11. `<options_premium>` (G, /100)
12. `<exchange_fee>` (G, /100)
13. `<forts_varmargin>` (G, /100)
14. `<varmargin>` (G, /100)
15. `<pclmargin>` (G, /100)
16. `<options_vm>` (G, /100)
17. `<spot_buy_limit>` (G, /100)
18. `<used_spot_buy_limit>` (G, /100)
19. `<collat_current>` (G, /100)
20. `<collat_blocked>` (G, /100)
21. `<collat_free>` (G, /100)

Итого 21 элемент; отправка — `FUN_1800dba40` (как у других колбэков).

## `<united_limits>` (D)

`FUN_180114c50`, printf-формат:
`<united_limits union="%1%"><open_equity>%2%</open_equity><equity>%3%</equity>
<requirements>%4%</requirements><free>%5%</free><vm>%6%</vm><finres>%7%</finres><go>%8%</go>
</united_limits>` (единый лимит по юниону; живой/оракульный случай не снимался — I).

## RQ_FORTS_FIRM_MONEY (47) — не восстановлено (I)

В DLL есть строки `RQ_FORTS_FIRM_MONEY` и `RQ_FORTS_FIRM_MONEY return incorrect buffer`,
а для записи RQ_MAIN тега `0x2f` есть обработчик `FUN_1803d1400`; **формат запроса/ответа и
состав полей не разобраны** (не восстановлено). `<forts_money>`-форма снята оракулом в рамках
T20 — см. `positions.md` (client/union/markets/market=4/blocked/varmargin/current/free,
строитель `FUN_18011dcc0`, литеры `0x18082e4c0`).

## Oracle-статус и фикстуры

- Попытка снять `<clientlimits>` оракулом: reply-инъекция type=43 (структура выше, 43 Б)
  пока даёт «RQ_CLIENT_LIMITS return incorrect buffer» — комбинация session-флагов (&2/&4) и
  наличие 2 хвостовых G в реплее не подтверждены; нужен 1–2 подбора или S-съёмка утром.
- Фикстуры `limits-*` будут сняты после подтверждения; сейчас в `native/records/testdata` их нет.
- I: точные условия вывода каждого элемента (ненулевое поле), опечатка `</cpblused>`,
  `fob_varmargin` смещение, формат RQ_FORTS_FIRM_MONEY.
## Payload запроса RQ_CLIENT_LIMITS (43) — D + O

Обработчик `FUN_1803b4d50`: `FUN_1801c2030(stream,1)`, затем `FUN_1801bebb0(stream, client_id)`
и `FUN_180355d50(...,0x2b,"RQ_CLIENT_LIMITS")`. Фактический payload (живой/реплейный захват):
`22 69 1a` = **`L client_id`** (packed 32-bit, 108178 → 3 байта). Т.е. запрос = один packed-L
с числовым id клиента (код клиента резолвится DLL из стора до отправки). Ответ — см. раздел выше.
