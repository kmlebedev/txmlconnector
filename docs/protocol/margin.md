# Margin: get_max_buy_sell_tplus, get_max_buy_sell, get_cln_sec_permissions, режим кредитования (Task 23)

Дата: 2026-11-05. Источник: RE-1 (декомпилятор DLL 6.47.2.26.4) + RE-3 (оракул: replay-server +
DLL под Wine, только loopback, без живых торговых команд). Статусы: **S** — демо-сервер,
**O** — оракул DLL, **D** — декомпилятор, **I** — вывод.

## Команды (D + O)

| Команда | Обработчик (D) | XML-ответ | Запросы в сеть |
|---|---|---|---|
| `get_max_buy_sell_tplus` | `FUN_180198260` → `FUN_180111390` | `<max_buy_sell_tplus client="…">…</max_buy_sell_tplus>` | нет (O/D) |
| `get_max_buy_sell` | `FUN_180198220` → `FUN_1801130c0` | `<max_buy_sell [client][union]>…</max_buy_sell>` | нет (O/D) |
| `get_cln_sec_permissions` | `FUN_1801981a0` → `FUN_1800f1ca0` | `<cln_sec_permissions …>…</cln_sec_permissions>` | нет (O/D) |

Все три команды — **локальный рендер** из уже загруженных сторов (клиенты, позиции, справочник
бумаг/ставки). В захватах C>S за прогоны с этими командами **нет** ни одного кадра 84/85/86/82/152:
типы запросов — только 1 (LOGIN), 46 (PWD), 50 (TIME), 9 (STARTUP), 5 (MAIN), 148 (HISTORY_EXT),
42 (HOLDINGS), 2 (LOGOUT) — **O**. В телах `FUN_180111390`, `FUN_1801130c0`, `FUN_1800f1ca0`
нет вызовов `FUN_180355d50` (построитель/отправитель запросов) — **D**.

### `get_max_buy_sell_tplus` — вход (D + O)

```xml
<command id="get_max_buy_sell_tplus" client="КОД">
  <security><seccode>SBER</seccode><market>1</market></security>
  <security>…</security>
</command>
```

- `client` — **атрибут** команды (`FUN_18040cf20`), обязателен; `union` не поддерживается
  (с `union` без client — O: `<result success="false"><message>Client not specified.</message></result>`).
- Список бумаг — дочерние `<security>` с элементами `<seccode>` + `<market>` (`FUN_18040c140`);
  альтернативно — элементы `<secid>N</secid>`; порядок ответа = порядок запроса (O: `379,26`).
- Неизвестный клиент — O: `Wrong client specifited.`; неизвестная бумага — O:
  `Failed to get symbol. seecode(SBER), market(99)`-подобный текст (D: сборка строки в
  `FUN_180111390`); нечисловой secid — O/D `Secid have to be a number.`;
  `Wrong secid precified.`; клиент типа 4 (MCT) — O: `Данная команда не может быть выполнена для MCT клиента`
  (строка CP1251 `0x180837648`, D).

### `get_max_buy_sell` — вход (D + O)

- Атрибуты `client` и/или `union`; хотя бы один обязателен — O:
  `Parameters "union" and "client" should not be empty`; неизвестный код — O:
  `{8} Неверное значение параметра 'szClient'.` / `… 'szUnion'.`.
- Список бумаг — как у tplus (`<security>`/`<secid>`); порядок сохраняется (O: SBER, GAZP).
- Клиент MCT: команда **успешна**, значения 0/0 (O) — в отличие от tplus, проверки типа нет (D).

### Выход обеих команд (D-литералы + O-формы)

```xml
<max_buy_sell_tplus client="TESTCLIEN"><security secid="26"><market>1</market><seccode>SBER</seccode><maxbuy>13210</maxbuy><maxsell>15210</maxsell></security></max_buy_sell_tplus>
<max_buy_sell client="TESTCLIEN"><security secid="26">…</security></max_buy_sell>
<max_buy_sell union="…"><security secid="26">…</security></max_buy_sell>
```

- Литералы (D): `<max_buy_sell_tplus client="` `0x18082d008`, `</max_buy_sell_tplus>`
  `0x18082d0d8`; `<max_buy_sell` `0x18082d758` (` client="`/` union="` —
  `FUN_18040cf20`), `</max_buy_sell>` `0x18082d920`; элементы `<security secid="…">`,
  `<market>`, `<seccode>`, `<maxbuy>`, `<maxsell>` — общие литералы в тех же функциях.
- `secid`, `market`, `seccode` берутся из справочника бумаг сессии, а не из запроса (запрос по
  `seccode+market` или `secid` даёт один и тот же XML — O).
- `maxbuy`/`maxsell` печатаются **целыми числами** (форматтер `FUN_1800cf330`; O: `13210`,
  `15210`, `0`), хотя в сторе это `double` (см. ниже).

### `get_cln_sec_permissions` (D + O)

Вход (все поля — **элементы**, не атрибуты):

```xml
<command id="get_cln_sec_permissions">
  <security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security>
  <client>КОД</client>          <!-- и/или <union>КОД</union> -->
</command>
```

- `<board>` **обязателен** внутри `<security>` — O: `<message>Не указан элемент "board"</message>`.
- Без `<client>`/`<union>` — O: `<message>Должен быть указан клиент и/или юнион</message>`
  (строка CP1251 `0x1808346b0`, D).
- Неизвестный клиент/юнион — O: `Неизвестный код клиента` / `Неизвестный код юниона`.
  Неизвестная бумага — O: `Инструмент не найден ('seccode'=NOSUCH, 'board='TQBR)`
  (текст DLL дословно, с «=`» внутри кавычек).
- Клиент MCT обрабатывается успешно (O).

Выход (O):

```xml
<cln_sec_permissions client="TESTCLIEN" secid="26">
  <security><seccode>SBER</seccode><market>1</market><board>TQBR</board></security>
  <tradeable>true</tradeable><marketable>true</marketable>
  <longable>true</longable><shortable>true</shortable>
  <regratelong>14.28</regratelong><regrateshort>14.28</regrateshort>
</cln_sec_permissions>
```

- Литералы (D): `<cln_sec_permissions` `0x180834708`, `</cln_sec_permissions>`
  `0x180834a18`; корень — `<cln_sec_permissions[ client="…"][ union="…"] secid="N">` (атрибут
  `secid` — из запрошенной бумаги).
- Права (D): битовая маска `m` из `FUN_18001a700` (вызовы в `FUN_1800f1ca0` @`0x1800f250c`,
  `0x1800f254f`); `tradeable ⇔ (m&0x11)==0x11`, `marketable ⇔ (m&0x22)==0x22`,
  `longable ⇔ (m&0x88)==0x88`, `shortable ⇔ (m&0x44)==0x44`.
- `marketable` зависит от записи `R_SEC_MKT_PERMIT` (0x0e): инъекция `0e001a00` перед командой
  даёт `<marketable>false</marketable>` (O; см. `marketord` ниже и `dictionaries.md`).
- Ставки (D): четыре `double`-поля из `FUN_18001a700` (пятый аргумент), в XML умножаются на
  `100.0` (`DAT_1808256c0`) и форматируются с 2 знаками (`FUN_180136800`):
  ветка `<regratelong>` = rate[2], `<regrateshort>` = rate[1] (O: `14.28`, т.е. 0.1428);
  вторая ветка — `<riskrate_long>`=rate[2], `<reserate_long>`=rate[3],
  `<riskrate_short>`=rate[1], `<reserate_short>`=rate[0]` (D; в O-прогонах не встречалась).
  При каких условиях выбирается ветка — D по `FUN_1800f1ca0` (условие по типу рынка/бумаги,
  точная семантика — **I**).

## `<marketord>` — допуск рыночных заявок (D + O)

PDF 4.35 называет это `<marketordallowed>`; фактический тег DLL — `<marketord>` (**D/O**).

- Запись RQ_MAIN `0x0e` `R_SEC_MKT_PERMIT` (безразмерная): `W secid, B val` (val ≠ 0 — «да»).
- Обработчик `FUN_1803c1360` (case 4 = тег 0x0e): `local_227 = val != 0`;
  `FUN_1800db7a0(conn+0x20, secid, permit)`; та же запись правит бит 0x04 флагов бумаги
  (`FUN_180264150`, см. `dictionaries.md`).
- Строитель `FUN_1800db7a0`: `<marketord secid="N" permit="yes|no" seccode="CODE"/>`
  (литералы `0x18082b430`, `0x18082b418` = `yes`, `0x18082b41c` = `no`, `0x18082b404` =
  `"/>`); `seccode` — из справочника (пусто → `????????????`).
- Событие приходит на **каждую** запись 0x0e, без дедупликации (**O**):
  `<marketord secid="26" permit="yes" seccode="SBER"/>`, затем `permit="no"`.
- Фикстуры: `marketord-inject.hex` + `marketord-dll.xml`, `marketord-repro.md`.

## Стор и вычисление maxbuy/maxsell (D + O + I)

### Откуда данные (D)

- `FUN_180111390` (tplus) вызывает `FUN_1803654d0(store, client, NULL, 0x28, collection, &out, errbuf)`;
  результат — стор с `count` @`+0xc8` и массивом записей @`+0xd0` (stride `0xf8`). Поля записи:
  `int key` @`+0x1c`, `seccode` @`+0x20`, `double maxbuy` @`+0xa8`, `double maxsell` @`+0xb0`
  (чтение — `FUN_180111390`); совпадение записи — `key == FUN_1801d38b0(market, symbol+0x1d)`
  и `strcmp(seccode)`, market бумаги ∈ {1, 0xf}.
- `FUN_1801130c0` (max_buy_sell) вызывает `FUN_18036dda0(store, collection, client, union)`;
  результат — `count` @`+0x44`, массив @`+0x48` (stride `0x38`): `int key` @`+0x0`,
  `seccode` @`+0x4`, `double maxbuy` @`+0x28`, `double maxsell` @`+0x30`.
- Обе функции — **локальное вычисление** (нет `FUN_180355d50`), используют сторы объекта
  соединения (+0x3b8): клиенты (`FUN_1803856b0` = store+0x60), магазин символов
  (`FUN_1803856f0` = store+0x30). Данные магазина пополняются записями RQ_MAIN: клиент —
  `0x02` R_CLN_NEW, позиции — `0x3b` R_CLIENT_ASSET (деньги: `sym=0xffff`), параметры/ставки
  бумаги — записи бумаги (в т.ч. `R_SEC_MKT_PERMIT` 0x0e). `FUN_1803654d0` также вызывается
  из портфельных построителей `FUN_180371a70`, `FUN_18010d290`, `FUN_180114c50`
  (`<united_limits>`), т.е. это общий расчёт маржинального стора (**D**).
- `FUN_18001a700` — расчёт прав (`m`) и ставок (ставки → 5-й аргумент, массив `double`); вызывается
  из `FUN_1800f1ca0` и др. (**D**).

### Эмпирическая модель (O + I)

Инъекции клиента/денег/позиции и ответы DLL (все — **O**, ставка R=0.1428 из
`<regratelong>`; денежные значения — в рублях, т.е. `G/100`):

| `open_balance` денег (G) | SBER-позиция (G) | maxbuy | maxsell |
|---|---|---|---|
| 0 | 0 | 0 | 0 |
| 30 000 000 (300 000 ₽) | 0 | 7207 | 7207 |
| 60 000 000 (600 000 ₽) | 0 | 14415 | 14415 |
| 0 | 1000 | 6002 | 8002 |
| 30 000 000 | 1000 | 13210 | 15210 |
| 30 000 000 | 2000 | 19213 | 23213 |
| 60 000 000 | 1000 | 20418 | 22418 |

Модель, согласующаяся со всеми точками (**I**):

```
maxbuy  = floor( money/(P·R) + pos·(1−R)/R )
maxsell = floor( money/(P·R) + pos·(1+R)/R )
```

где `money` — свободные деньги, `pos` — позиция по самой бумаге (в единицах G записи `0x3b`,
для SBER lot=1), `P` — цена бумаги, `R` — ставка (0.1428). Для наблюдаемых точек
`P ≈ 291.46` (SBER) — совпадает с последней ценой SBER в реплейной сессии (закрытие последней
свечи `0x09` = 291.46 / цитата стора); **источник P (поле стора) не идентифицирован — I**.
Для GAZP при тех же 300 000 ₽ DLL отдаёт 3434 (O), т.е. P/R у GAZP иные — отдельных прогонов
с деньгами/позицией GAZP не делали (**I**).

- `maxbuy == maxsell` при отсутствии позиции по бумаге (**O**, все прогоны).
- Позиция по **другой** бумаге: инъекция 1000 GAZP при запросе SBER дала `maxbuy` +2309,
  `maxsell` +0 (O: 9516/9516 против 7207/7207); правило кросс-бумажного вклада не восстановлено
  (**I**). Для реализации DLL-faithful нужен отдельный RE-проход.
- Какой из `open_balance`/`balance` (mask 0x01/0x02) используется для `money`: в экспериментах
  оба поля были равны — разделить не удалось (**I**).
- С какими правами/типами клиента tplus считается — проверены типы 2 (leverage) и 4 (MCT; ошибка
  tplus / нули max_buy_sell, **O**); типы 0/1/3 не проверялись (**I**).

## `RQ_MARGIN_POS` (84) и `RQ_MARGIN_POS_LIST` (85) — D, в 6.47 недостижимы

Форматы восстановлены полностью (RE-1), но **ни одна команда и ни один рабочий поток DLL их не
шлёт** — мёртвый код в этой версии:

- у `FUN_180383100` (84) и `FUN_180362600` (вызывающий 85) нет ни одного `call rel32`,
  `lea`/`mov imm64` в `.text`, нет указателей в `.data`/`.rdata` и нет relocations — только
  записи `.pdata` (unwind) — проверено скриптами `find-calls.py`/`find-ptr.py`/`find-reloc.py`;
- в C>S всех записанных сессий (в т.ч. 9 дампов) нет кадров 84/85/86/82 (**D/O**).

**RQ_MARGIN_POS (84), обменник `FUN_180383100`:**

```
запрос:  FUN_1801c2030(stream,1); L client_id; W secid; B flag;  ->  FUN_180355d50(…,0x54,"RQ_MARGIN_POS",-1)
ответ:   longlong-указатель на структуру 0x90 Б (FUN_180455828(0x90)), поля:
  ветка «обычного» клиента (тип не 4/5 и проходит FUN_1801d9170/FUN_1801dcce0):
    G +8, G +0x10, W +0x18, W +0x40, W +0x1a, G +0x20, G +0x28, G +0x30, G +0x38,
    W +0x80, W +0x82, W +0x84, W +0x86, B +0x88;
    если B+0x88 != 0: G +0x48, +0x50, +0x58, +0x60, +0x68, +0x70, +0x78
  ветка типа 4/5 (mct/margin_level):
    W +8, G +0x10, G +0x18, G +0x20, G +0x28, B +0x88;
    если B+0x88 != 0: G +0x30, +0x38, +0x40, +0x48, +0x50, +0x58, +0x60, +0x68,
    +0x70, +0x78, +0x80
  ошибки: "RQ_MARGIN_POS write failed" / "MarginPosRequest return incorrect buffer"
```

Семантика полей ответа не установлена (**I**): обменник никем не вызывается, поэтому оракул
(инъекция reply) невозможен — DLL не отправит запрос.

**RQ_MARGIN_POS_LIST (85), обменник `FUN_180382940`:**

```
запрос:  FUN_1801c2030(stream,1); L client_id; B flag;
         для каждого элемента списка (stride 0x30, W secid @+0): W secid;
         -> FUN_180355d50(…,0x55,"RQ_MARGIN_POS_LIST",-1)
ответ:   для каждого элемента списка: 7 × packed G -> +0x28, +0x30, +0x38, +0x40, +0x48, +0x50, +0x58
  ошибки: "RQ_MARGIN_POS_LIST write failed" / "MarginPosRequestList return incorrect buffer"
```

Вызывается только из `FUN_180362600` (расчёт маржи по клиенту/юниону; сам недостижим) — **D**.

## Режим кредитования (PDF 3.17/4.14–4.16) — D, без O

- Команды `get_leverage_control` в таблице DLL 6.47 нет (см. `commands.md`).
- Литерала `<leverage>`/`<marketordallowed>` в DLL нет (D: поиск строк). Вместо этого:
  - допуск рыночных заявок — событие `<marketord …/>` (см. выше, PDF 4.35);
  - параметры кредитования приходят в ответе **RQ_INQUIRE_UT (152)** (`FUN_1803dc4e0`), который
    разбирается как XML-документ: элементы `market`, `instrclass-defaults`, `instruments`,
    атрибуты `name`, `min_ratio`, `leverage` (`FUN_180019000`): `name` → conn+0xc0,
    `min_ratio` → conn+0xe0 (double), `leverage` → conn+0x100 (double) — **D**.
  - O-прогон RQ_INQUIRE_UT не снимался (нет записанного ответа 152) — **I**.

## Фикстуры (`native/records/testdata`)

| Файл | Что | Статус |
|---|---|---|
| `margin-inject.hex` | 80 Б: `0x02*` клиент TESTCLIEN (id 4096, leverage) + `0x3b*` деньги 300 000 ₽ + `0x3b*` SBER 1000 | O |
| `margin-dll.xml` | ожидаемые колбэки трёх команд: `<max_buy_sell>`, `<max_buy_sell_tplus>`, `<cln_sec_permissions>` (по строке на колбэк) | O |
| `marketord-inject.hex` | 8 Б: две записи `0x0e` R_SEC_MKT_PERMIT SBER: permit=yes, permit=no | O |
| `marketord-dll.xml` | два колбэка `<marketord …/>` (yes, no) | O |
| `margin-repro.md`, `marketord-repro.md` | происхождение, обезличивание, команды воспроизведения | — |

Значения фикстуры `margin-dll.xml` получены при `money=300 000 ₽`, `pos=1000 SBER`,
ставке 0.1428 и цене SBER из реплейной сессии; при другой сессии/бумаге значения будут иными
(фикстура воспроизводима только с dump из `.re-run/r14/` — вне git).

## I: что не восстановлено

1. Формула maxbuy/maxsell — эмпирическая модель согласуется с 7 точками, но точный код
   (`FUN_1803654d0`/`FUN_18036dda0`) не разобран до конца: источник цены P, поле денег
   (`open_balance` vs `balance`), кросс-бумажный вклад позиций, обработка типов клиентов 0/1/3.
2. Семантика полей ответов RQ_MARGIN_POS (84) / RQ_MARGIN_POS_LIST (85): оракул невозможен
   (обменники недостижимы), живьём не наблюдались.
3. Вторая ветка ставок `<riskrate_long>/<reserate_long>/<riskrate_short>/<reserate_short>` —
   только D (в O-прогонах DLL выбрала `<regratelong>/<regrateshort>`).
4. `<max_buy_sell union="…">` с **валидным** юнионом не проверен: в записанной демо-сессии
   юнионов нет (только O-ошибки для несуществующего кода).
5. RQ_INQUIRE_UT (152) XML-конфиг `leverage`/`min_ratio` — D, без O.
6. Живой S-прогон (демо) не делался: RE-3 loopback достаточен для команд (неторговые), но
   демо-сервер мог бы подтвердить поведение для реальных клиентов/юнионов.

## Источники

D: `tools/re/ghidra/XrefDecomp.java` по `txmlconnector64-6.47.2.26.4.dll` (проект
`${TMPDIR}/txc-ghproj`), функции и адреса — в тексте выше.
O: `tools/replay-server` + `tools/tcproxy` + `tools/re/run_dll.sh`-вариант под Wine 11.21,
DLL 6.47.2.26.4, dump `.re-run/r14/decode-full.txt` (вне git), инъекция RQ_MAIN после RQ_HISTORY_EXT
(148). Команды — `native/records/testdata/{margin,marketord}-repro.md`.
