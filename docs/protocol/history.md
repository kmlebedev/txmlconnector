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

### Данные: запись 0x09 (S/D, частично I)

Свечи приходят в RQ_MAIN записью **тега 0x09** (обработчик `FUN_1803bcf20`; лог
`ReadHistory: incorrect buffer`). Заголовок записи (D): `W secid, W board, B period, B флаг`;
далее по 7 packed-L на свечу (+1 L, если флаг ≠ 0 — вероятно openinterest / только futures; I).
Внутри 7 значений — дельта-сжатые OHLCV относительно предыдущей свечи (**I**, точный порядок
полей ещё требует отдельного прохода; значения цен вида 29146 и объёмов 1187 видны в потоке).
Разбор мини-фикстуры: запись 124 Б ↔ 10 свечей 04.11.2026 18:35–18:44 (S).

XML: `<candles secid="26" board="TQBR" seccode="SBER" period="1" status="1">` +
`<candle date="04.11.2026 18:35:00" open="291.42" close="291.39" high="291.43" low="291.36"
volume="1173"/>` …; строители XML — `FUN_180101e20` (обёртка) и `FUN_180130670` (candle).
`status`: 0 — данных больше нет, 1 — заказанное количество выдано, 2 — продолжение (следующая
порция), 3 — данных нет (PDF 4.1).

**S-наблюдения:** на демо `count=10` → один колбэк `status="1"` с 10 свечами; `count=1000` → один
колбэк `status="1"` с ~1000 свечами (запись 11 783 Б), **status=2 в этих прогонах не встречался**
(многочастная выдача на демо не воспроизведена — для `TestHistoryCollectsParts` нужна
сконструированная/записанная сессия, I).

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

### Данные тиков (S-негатив)

В вечерней сессии (21:00–21:50 МСК) на подписки SBER/GAZP/SiZ6 (filter true и false, tradeno=0)
DLL **не отправила ни одного RQ_TICKS (13) и не выдала ни одного `<ticks>`** за 25–45 с;
подписка при этом принята (`<result success="true"/>`, RQ_SUBSCRIBE_TICKS ушёл). Похоже, тики
демо-сервер отдаёт только в основную сессию/при новых сделках — **S-негатив**, для
`TestTicksMatchDLL` нужен захват в основную сессию или oracle-инъекция (I).

## Фикстуры (native/records/testdata/)

| Файл | Что внутри | Статус |
|---|---|---|
| `candles-live.hex.gz` | gzip(hex): 124 Б — запись 0x09 (10 свечей SBER period=1) из r14 | S |
| `candles-dll.xml.gz` | gzip(text): колбэк `<candles …status="1">` с 10 `<candle>` | S |
| (тики) | не сняты — S-негатив вечерней сессии; формат RQ_SUBSCRIBE_TICKS — D/S выше | — |

Большие части (count=1000: запись 11 783 Б и колбэк ~107 КБ) и запросы
`01 001a 0025 01 000a 18 00`/`01 001a 0025 01 03e8 18 00` — в `.re-run/r14/`, в git не кладутся.

## Не проверено

- Точный порядок/типы 7 packed-L на свечу (дельта-сжатие OHLCV) — I; нужен ещё один проход
  по `FUN_1803bcf20` (или oracle-инъекция записей 0x09).
- Многочастная выдача свечей (`status=2`) — не воспроизведена на демо.
- Тики: живых данных нет (S-негатив), формат записи тиков и RQ_TICKS не подтверждены.
