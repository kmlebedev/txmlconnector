# История (gethistorydata) и тики (subscribe_ticks): S/D/I

Дата: 2026-11-04 (вечерняя сессия), демо-сервер Finam, DLL `txmlconnector64-6.47.2.26.4.dll`
под Wine 11.21 через `tools/tcproxy`. Живые захваты: `.re-run/r14`, `.re-run/r15`, `.re-run/r16`
(вне git). Статусы: **S** — демо-сервер, **D** — декомпилятор, **I** — вывод.

## gethistorydata (Task 13, RQ_HISTORY 11 / RQ_HISTORY_EXT 148)

### Команда (S)

Обработчик `FUN_180198020` → `FUN_1800ee460`. Принимаются обе формы (обе проверены живьём):

```xml
<command id="gethistorydata">
  <security><board>TQBR</board><seccode>SBER</seccode></security>
  <period>1</period><count>10</count><reset>true</reset>
</command>
<command id="gethistorydata" secid="26" period="1" count="1000" reset="true"/>
```

Форма с атрибутами требует уже установленного соединения: попытка сразу после `connect` вернула
`Cannot process this command without connection.` — **S**. `period` — id из `<candlekinds>`,
`count` — сколько свечей, `reset` — начать заново. Ошибки: `'period' is absent`,
`'period isn't a number.`, `Incorrect period value` (D).

### Запрос на проводе (S/D)

DLL использует **RQ_HISTORY_EXT (148)**, RQ_HISTORY (11) в живом прогоне не отправлялся.
Payload (10 байт, live): `01 001a 0025 01 000a 18 00` для count=10 и
`01 001a 0025 01 03e8 18 00` для count=1000:

```
B reset(1)  W secid  W board  B period  W count  L -1  L 0   (последние два — I: продолжение/флаги)
```

Строители: `FUN_1803b5c30` (RQ_HISTORY_EXT 148) и `FUN_18035a910` (RQ_HISTORY 11; в живой
команде не наблюдался). Оба шлют через `FUN_180355d50`; ответ сервера на 148 — пустой payload
(S), свечи приходят отдельно через RQ_MAIN.

### Данные: запись 0x09 (S/D/O — layout уточнён оракулом)

Свечи приходят в RQ_MAIN записью **тега 0x09** (обработчик `FUN_1803bcf20`, лог
`ReadHistory: incorrect buffer`). Заголовок: `W secid, W board, B period, B флаг`. Далее свечи в
порядке **от новых к старым**, каждая — **7 packed-L** (O: RE-3 через `tools/replay-server` +
инъекция сконструированных записей в DLL под Wine):

| # | Поле | Смысл (O) |
|---|---|---|
| 1 | время | для первой свечи записи — **минуты Unix** (29896964 → `04.11.2026 18:44:00`); для последующих — дельта (наблюдалось −1); накопление времени в многозаписной выдаче — I |
| 2 | open | абсолютная цена в единицах 10^-decimals (29146 → `291.46`) |
| 3 | close | `open + delta` (−3 → `291.39`) |
| 4 | high | `open + delta` (+1 → `291.43`) |
| 5 | low | `open + delta` (−6 → `291.36`) |
| 6 | ? | встречались 15–48; в XML для акций не выводится (вероятно число сделок / дельта OI) — I |
| 7 | volume | абсолютный объём (1187) |

Флаг ≠ 0 → +1 L на свечу (вероятно openinterest, только для фьючерсов/опционов) — I. Запись
заканчивается одним L **status** ∈ [0..3]: 0 — данных больше нет, 1 — порция выдана, 2 —
продолжение, 3 — данных нет (PDF 4.1).

XML: строители `FUN_180101e20`/`FUN_180130670`; `<candles secid=… board=… seccode=… period=…
status=…>` + `<candle date="04.11.2026 18:44:00" open="291.46" close="291.46" high="291.46"
low="291.41" volume="1187"/>`; порядок свечей в колбэке — **обратный записи** (от старых к новым).

**Многочастная выдача (O):** две записи 0x09 с `status=2` (часть 1) и `status=1` (часть 2) → DLL
**не отдаёт промежуточный колбэк**: копит свечи и отдаёт один `<candles status="1">` со свечами
обеих частей в хронологическом порядке. Именно это должен повторить native-клиент для
`TestHistoryCollectsParts`.

**S-наблюдения:** `count=10` → один колбэк `status="1"` с 10 свечами; `count=1000` → один колбэк
`status="1"` с ~1000 свечами (запись 11 783 Б); `status=2` на демо не встречался — воспроизведён
оракулом (O).

## subscribe_ticks (Task 14, RQ_SUBSCRIBE_TICKS 120, RQ_TICKS 13)

### Команда (S/D)

Обработчик `FUN_1801986e0` → `FUN_1800ec970`. Рабочая форма (проверена живьём, r15/r16):

```xml
<command id="subscribe_ticks">
  <filter>true</filter>
  <security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security>
</command>
```

- `filter` — дочерний элемент команды (значение `true`/иное; `true` → фильтр по tradeno);
  в форме без элементов `security` допускается атрибут `filter` у `<command>` (D, r14:
  команда без security отправила маску 0 и ничего не подписала — S).
- у каждого `security` обязателен **дочерний элемент** `tradeno` (D: иначе
  `Cannot find tradeno element.`); при другой ветке разбора встречается и атрибут
  `tradeno` (`Cannot find tradeno attribute.`) — I.
- `unsubscribe_ticks` DLL не знает: `Unknown command.` (S); отписка, вероятно, повторным
  `subscribe_ticks` с фильтром/без элементов (I).

### Запрос на проводе (S/D)

RQ_SUBSCRIBE_TICKS (120), payload (live): `01 001a 0025 00` для одной бумаги и
`00 001a 0025 00 017b 0025 00 5067 002a 00` для трёх — формат:
`B filter; { W secid; W board; G tradeno }*` (строитель `FUN_1803ba530`, **D+S**).
RQ_TICKS (13) — опрос тиков (строитель `FUN_1803c0ac0`; в захвате 13 не встречался, формат
по D: `B,B,W,W,G…`, I).

### Ответ RQ_TICKS (13) и запись тика — D (оракул заблокирован)

RQ_TICKS — **прямой reply-кадр** (type 13), а не запись RQ_MAIN: разбор встроен в обменник
`FUN_1803c0ac0` (тот же, что строит запрос: `FUN_180355d50(..., 0xd, "RQ_TICKS")`).

Запрос (D; живой payload — S): `B filter; { W secid; W board; G tradeno }*`, байты пишутся в
обратном порядке списка; live: `01 001a 0025 00`.

Ответ (D, `FUN_1803c0ac0`, строки парсера 1469–1523): последовательность до `W secid == 0xffff`:

```
W secid; W board; G tradeno; L time; L price; B ?; L ?
```

Чтение: `FUN_1801bd5d0` (secid), `FUN_1801bd380` (board), `FUN_1801bd9b0` (tradeno G),
`FUN_1801bd7b0` (time, price), `FUN_1801bd290` (байт), `FUN_1801bd7b0` (объём). Далее —
накопление tradeno/time/price относительно предыдущего тика и запись в очередь `FUN_1803e67f0`;
XML `<ticks>`/`<tick>` имеют литералы `0x180836fb8`/`0x180832da8` (точный строитель не
локализован).

**Оракул RE-3 сейчас невозможен без доработки инструмента:** `tools/replay-server -inject`
подменяет только тело ответа **RQ_MAIN**, а RQ_TICKS — самостоятельный reply-кадр; чтобы снять
`<ticks>` у DLL, нужна инъекция reply-кадра (расширение `native/replay`/`replay-server`) либо
живой прогон в основную сессию (5 ноября после 10:00 МСК).

**S-негатив:** вечерняя сессия 4 ноября: subscribe_ticks принят, RQ_SUBSCRIBE_TICKS ушёл, но
ни RQ_TICKS (13), ни `<ticks>` за 25–45 с не было — фикстуры тиков отложены до основной сессии
(или до расширения оракула).


## Фикстуры (native/records/testdata/)

| Файл | Что внутри | Статус |
|---|---|---|
| `candles-live.hex.gz` | gzip(hex): 124 Б — запись 0x09 (10 свечей SBER period=1) из r14 | S |
| `candles-dll.xml.gz` | gzip(text): колбэк `<candles …status="1">` с 10 `<candle>` | S |
| `candles-part1-inject.hex` | hex: одна запись 0x09 (status=2, 1 свеча 18:44) — часть 1 | O |
| `candles-part2-inject.hex` | hex: одна запись 0x09 (status=1, 1 свеча 18:43) — часть 2 | O |
| `candles-multipart-dll.xml` | ожидаемый итоговый колбэк DLL (status=1, обе свечи 18:43/18:44) | O |
| (тики) | не сняты — S-негатив вечерней сессии; формат RQ_SUBSCRIBE_TICKS — D/S выше | — |

Большие части (count=1000: запись 11 783 Б и колбэк ~107 КБ) и запросы
`01 001a 0025 01 000a 18 00`/`01 001a 0025 01 03e8 18 00` — в `.re-run/r14/`, в git не кладутся.

## Не проверено

- Поле 6 на свечу и смысл дополнительного L при флаге ≠ 0 (openinterest?) — I.
- Накопление времени в многозаписной выдаче (когда дельта, когда абсолют) — I; в фикстурах
  каждая часть начинается со своего абсолютного времени.
- Многочастная выдача воспроизведена оракулом (O), но не на живом демо (S-негатив).
- Тики: живых данных нет (S-негатив), формат записи тиков и RQ_TICKS не подтверждены.
