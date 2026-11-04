# Опционы: get_option_families, get_family_strikes, get_options — RQ 149/153/150/151 (Task 27)

Дата: 2026-11-05. Источник: RE-1 (декомпилятор DLL 6.47.2.26.4) + RE-3 (оракул: replay-server +
DLL под Wine, только loopback). Статусы: **S** — демо-сервер, **O** — оракул DLL, **D** —
декомпилятор, **I** — вывод. Живой демо-сервер по указанию не использовался.

## Команды (D + O)

| Команда | Обработчик (D) | Запрос | XML |
|---|---|---|---|
| `get_option_families` | `FUN_1801982a0` → `FUN_1800f30d0` | RQ_OPTION_FAMILIES (149) | `<option_families>` (D) |
| `get_family_strikes` | `FUN_1801981e0` → `FUN_1800f3450` | RQ_FAMILY_STRIKES (153) | `<family_strikes>` (D) |
| `get_options` | `FUN_1801982e0` → `FUN_1800f2e60` | RQ_CERTAIN_OPTIONS (151), RQ_FAMILIY_OPTIONS (150) | `<option …>`/`<options_failed>` (D) |

Все три — **неторговые**. O-прогоны на записанной сессии: `get_option_families` **success**
(отправляется 149), `get_family_strikes` → `<result success="false"><message>Unsupported request.</message></result>`,
`get_options` → `<result success="false"/>` без сообщения.

### Входные XML (D)

- `get_option_families`: необязательный элемент `<security>` (CSecId: `board`+`seccode` или
  `secid`); без него — SecIdStrategy из атрибутов команды (`FUN_1800c0720`).
- `get_family_strikes`: необязательный `<security>` + **обязательный** `<mat_date>` в формате
  `%d.%m.%Y`. Ошибки (D+O): `No mat_date found`; `Failed to parse mat_date=…`.
- `get_options`: один или несколько элементов `<opt_code>…</opt_code>` (список кодов опционов);
  пусто → `No seccodes found` (D).

### Гейт версии сервера (D + O)

- В `FUN_1803b9950` (153) и `FUN_1803b9cd0` (150/151) проверяется
  `*(int*)(symbolStore + 0x694) < 0x45` → текст `Unsupported request.`
  (то же в кэше-рендере `FUN_1803583e0`/`FUN_180358460`).
- В записанной сессии версия = **68 (0x44)** → `get_family_strikes` и `get_options` недоступны:
  O-наблюдение `Unsupported request.` / `success=false`; `get_option_families` (149 гейта не
  имеет) работает. Где именно хранится поле +0x694 (какой кадр/поле RQ_STARTUP/RQ_LOGIN) — **I**;
  для полного oracle по 153/150/151 нужно подделать версию сервера в дампе.

## RQ_OPTION_FAMILIES (149) — D + O

- Построитель `FUN_1803b92b0`: `FUN_1801c2030(stream,1)`; `W secid` (`FUN_1801be710`);
  `FUN_180355d50(...,0x95,"RQ_OPTION_FAMILIES")`.
- **O (payload):** `00 1a` = `W secid=26` (2 байта) — в C>S `type=149 len=2 001a`.
- Ответ: C-строка до 0x200 байт (`FUN_1801bd170`): **пустая** → успех; **непустая** → текст ошибки
  (O: reply `00` → `<result success="true"/>`; reply `"No families"\0` →
  `<result success="false"><message>No families</message></result>`).
- Данные (семейства) приходят записями RQ_MAIN **R_FAMILY 0x6f** (см. ниже) и рендерятся в
  `<option_families>`…`</option_families>` (D-литералы; O-форму снять не удалось — I).

## RQ_FAMILY_STRIKES (153) — D

- Построитель `FUN_1803b9950`: `FUN_1801c2030(1)`; `W secid`; `L family_id`;
  `FUN_180355d50(...,0x99,"RQ_FAMILY_STRIKES")`. Ответ — C-строка до 0x200 (пусто → успех,
  иначе ошибка). Гейт версии ≥0x45.

## RQ_CERTAIN_OPTIONS (151) / RQ_FAMILIY_OPTIONS (150) — D

- 151 (`FUN_1803b9cd0`): для каждого элемента списка опционов — `FUN_1801c2030(1)` +
  `Z25 opt_code` (строка до 25), затем `FUN_180355d50(...,0x97,"RQ_CERTAIN_OPTIONS",-1)`.
- 150 (`FUN_1803b9600`): `FUN_1801c2030(1)`; `W secid`; `Z6 str`; `L`; `L`;
  `FUN_180355d50(...,0x96,"RQ_FAMILIY_OPTIONS")`; ответ — C-строка до 0x200.

## Записи RQ_MAIN опционов (D)

- **0x6f R_FAMILY** (обработчик `FUN_180396510`): `W secid`, `Z6 opt_code`, `L`,
  `L`; структура (532 Б) сохраняется в стор `symbolStore+0x6c8` по secid.
- **0x71 R_END_OF_FAMILY** (`FUN_180396860`): `W secid`, `Z6 opt_code`, `L`,
  `Z512 text`; кладётся в `conn+0x2b8`; судя по литералам — триггер `<options_failed>`
  для ошибок (I, не воспроизведён).
- **0x78 R_STRIKES** (`FUN_180396d10`): `W secid`, `L family_id`, далее по страйкам
  `Z26 opt_code`, `B cp`, `L price` (пустой `Z26` — конец списка), затем `B final`;
  стор `(secid,family_id)` в `conn+0x2d0`.
- Ошибки-строки (D): `R_FAMILY: incorrect buffer`, `R_END_OF_FAMILY: incorrect buffer`,
  `R_STRIKES: incorrect buffer 1..4`.

## XML-формы (D-литералы)

- `<option_families>` `0x1808373b0`, `</option_families>` `0x180837398`, `<family>`
  `0x180837388`, `</family>` `0x180837378`, `<mat_date>` `0x180837368`,
  `</mat_date>` `0x180837358`, `<lot_size>` `0x180837348`, `</lot_size>` `0x180837338`.
- `<family_strikes>` `0x180837470`, `</family_strikes>` `0x180837588`, `<strike>`
  `0x180837520`, `</strike>` `0x180837578`, `<opt_code>` `0x180837568`,
  `</opt_code>` `0x180837558`, `<price>` `0x180837550`, `</price>` `0x180837540`,
  `<cp>` `0x180837534`, `</cp>` `0x18083752c`, `<mat_date>`/`</mat_date>` — те же.
- `<options_failed>` `0x1808371a0`, `</options_failed>` `0x1808371e0`,
  `<option opt_code="` `0x1808371c8`, `" error="` `0x1808371b8`, `"/>` `0x1808371b4`;
  также `<security seccode="…" board="…" secid="…"/>` (`0x1808372d0`/`0x1808372e8`/`0x1808372f8`/`0x180837304`)
  и `<message>`/`</message>` (0x180837318/0x180837308).
- Полная точная форма (порядок/атрибуты) — **I**: рендер живёт в недоступном для декомпиляции
  большом генераторе `FUN_1800f7b40` (response buffer exceeded); O-снять формы не удалось
  (см. I).

## I: что не восстановлено / не снято

1. **O-формы XML опционов не сняты.** Инъекция RQ_MAIN-записей R_FAMILY/R_END_OF_FAMILY/R_STRIKES
   (до и после команды, модифицированный dump; fake reply 149) не даёт колбэков
   `<option_families>`/`<family_strikes>` — обработка, видимо, гейтится состоянием
   «запроса семейств» в DLL (кэш-механика `FUN_180358230/1803583e0/180358460` не разобрана).
2. Гейт версии 0x45: для 153/150/151 нужен фейковый сервер версии ≥0x45 в дампе; где хранится
   `+0x694` — **I** (не найдено записей поля в декомпиляции).
3. Точная XML-форма `<family>` (какие поля внутри: mat_date/lot_size/opt_code — по литералам),
   `<strike>`, `<option …>`/`<options_failed>` — **I** (только литералы D).
4. Триггер `<options_failed>` / семантика `R_END_OF_FAMILY.text` — **I**.
5. Общий генератор `FUN_1800f7b40` декомпилировать не удалось (buffer exceeded) —
   при необходимости резать на части/использовать оракул.

## Фикстуры (`native/records/testdata`)

| Файл | Что | Статус |
|---|---|---|
| `option-families-request.hex` | payload запроса RQ_OPTION_FAMILIES (149): `W secid=26` = `001a` | O |
| `option-families-reply-ok.hex` | reply 149 = пустая C-строка `00` → success | O |
| `option-families-reply-error.hex` | reply 149 = `"No families"\0` → error с этим текстом | O |
| `option-families-repro.md` | происхождение и воспроизведение | — |

XML-фикстур (`option-*-dll.xml`) нет — честный **I** (см. выше).

## S / не проверено

- **S-фактов нет**: живой демо-сервер не использовался по указанию; на записанной сессии версия
  сервера 68 → 153/150/151 недоступны.
- Реальная выдача семейств/страйков/опционов и формы XML — только D/I; утром в S-серии можно
  снять, если демо-сервер поддерживает (нужна версия ≥0x45).

## Источники

D: `tools/re/ghidra/XrefDecomp.java` по `txmlconnector64-6.47.2.26.4.dll`; функции — выше.
O: `tools/replay-server` + `tools/tcproxy` + DLL под Wine 11.21, dump `.re-run/r14/decode-full.txt`
(вне git), модификация дампа — `.re-run/t49/mk-opt-dump.py`; прогоны `.re-run/t49/opt{A,B,C,D}`.
