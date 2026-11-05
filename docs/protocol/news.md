# Новости: get_old_news, get_news_body, R_NEWS 0x35 / R_NEWS_UPLOAD 0x36, <news_header>/<news_body> (Task 24)

Дата: 2026-11-05. Источник: RE-1 (декомпилятор DLL 6.47.2.26.4) + RE-3 (оракул: replay-server +
DLL под Wine, только loopback). Статусы: **S** — демо-сервер, **O** — оракул DLL, **D** —
декомпилятор, **I** — вывод. Живой демо-сервер по указанию не использовался.

## Команды (D + O)

| Команда | Обработчик (D) | Запрос | Ответ |
|---|---|---|---|
| `get_old_news` | `FUN_1801980a0` → `FUN_1800edcb0` | RQ_GETNEWS (73) | заголовки приходят записями RQ_MAIN 0x35/0x36 → колбэки `<news_header>` |
| `get_news_body` | `FUN_180198060` → `FUN_1800ed8b0` | RQ_GETNEWS (73) | тело → колбэк `<news_body>` (O не снят, см. I) |

Обе команды неторговые; в C>S реально уходит **один** кадр RQ_GETNEWS (73) на команду — **O**
(по 2 кадра 73 в прогоне с двумя командами).

### `get_old_news` (D + O)

- `count` — **атрибут** команды (`FUN_18040d070`): обязателен, целое 1..100. Ошибки (O, дословно):
  `"count" attribute not found`, `News count must be in interval from 1 to 100. Received value is N`,
  `"count" attribute must be an integer` (D).
- Хендлер берёт из стора новостей последний id (`FUN_1800c9a30`), формирует запрос:
  `L from_id = max(0, lastId - count)`, `L to_id = lastId` (если стор пуст — `0, 0x7fffffff`),
  `B count`, `B 1` (**D**; O-пейлоады ниже).
- O (прогон `.re-run/t49/news-final`, в сторе заголовки 1001 и 2002):
  `<command id="get_old_news" count="2"/>` → success; кадр C>S: `71 3e 91 3e 02 01` =
  `L 999, L 1001, B 2, B 1`.
- O (прогон `.re-run/t49/news2`, в сторе только загруженный заголовок 2002):
  `L 2000, L 2002, B 2, B 1` (`01 7d 21 7d 02 01`).

### `get_news_body` (D + O)

- `news_id` — **атрибут** команды (`FUN_18040cf20`): обязателен, целое 0..2147483647.
  Ошибки (O, дословно): `"news_id" attribute not found`;
  `Attribute "news_id" must be in interval from 0 to 2147483647`;
  `Header with id = N not found` (если заголовка нет в сторе).
- Хендлер ищет заголовок в сторе (`FUN_1800c9910`); если нет — ошибка выше; если есть, формирует
  запрос: `L news_id`, `L news_id`, `B 1`, `B 2` (**D**; O: `21 7d 21 7d 01 02` =
  `L 2002, L 2002, B 1, B 2`).
- O: `<command id="get_news_body" news_id="2002"/>` при заголовке 2002 в сторе → success и кадр 73.

## RQ_GETNEWS (73): построитель (D)

`FUN_1803b2d20(conn, stream, param3[12])`:

```
FUN_1801c2030(stream,1)
L param3[0]           // from_id / news_id
L param3[1]           // to_id   / news_id
B param3[8]           // count (для get_old_news) / 1 (для get_news_body)
B param3[9]           // 1 (old_news) / 2 (news_body) — режим запроса
FUN_180355d50(...,0x49,"RQ_GETNEWS",-1)
```

Ошибки записи (D-строки): `RQ_GETNEWS write failed`.

## Записи RQ_MAIN (D + O)

### 0x35 R_NEWS — заголовок новости (без размера)

Тело (диспетчер `FUN_18038d7d0`, case tag-1=0x34; обработчик читает подряд):

```
L  news_id
L  timestamp          (Unix-секунды; печатается локальным временем — O/D)
Z16 source            (C-строка до 16 симв.)
Z128 title            (C-строка до 128 симв.)
B  flag               (0 в O-прогонах; назначение — I)
```

Заголовок попадает в очередь колбэков `conn+0x248`; генератор `FUN_1800f7b40` →
`FUN_18011d740` → `FUN_18011cd90` отдаёт колбэк `<news_header>` (O).

### 0x36 R_NEWS_UPLOAD — заголовок/тело (без размера)

```
B  flags
   flags & 1:  L news_id, L timestamp, Z16 source, Z128 title, B   // заголовок
   flags == 2: L news_id                                          // id для тела
   flags & 2:  L package_len, package[package_len]                 // тело
```

- Заголовок из 0x36 (flags&1) также даёт `<news_header>` (O: id 2002 в фикстуре).
- Тело (flags&2) кладётся в очередь `conn+0x260`; `package` — упакованное тело:
  `int32 uncompressed_size` + zlib-поток (D: распаковка `FUN_1800c95a0` →
  `FUN_180451c10(dst, &dstLen, src+4, srcLen)`), ошибки D: `Failed to unpack body! Wrong package
  length (news_id = N)`, `Failed to unpack body! Buffer allocation error (news_id = N)`.

## XML-формы (D-литералы + O)

`<news_header>` — строитель `FUN_18011cd90`:

```xml
<news_header><id>N</id><timestamp>DD.MM.YYYY HH:MM:SS</timestamp><source><![CDATA[SRC]]></source><title><![CDATA[TITLE]]></title></news_header>
```

Литералы (D): `<news_header><id>` `0x180834308`, `</id><timestamp>` `0x1808342f0`,
`</timestamp><source><![CDATA[` `0x1808342d0`, `]]></source><title><![CDATA[` `0x1808342b0`,
`]]></title></news_header>` `0x180834290`; формат времени `%d.%m.%Y %H:%M:%S`
`0x180834278` (O: 1762300000 → `04.11.2025 23:46:40` — DLL печатает WALL-время как UTC-календарь;
локальная конверсия машины — **I**, в захватах новостей нет).

`<news_body>` — строитель `FUN_18011d1b0`:

```xml
<news_body><id>N</id><text><![CDATA[TEXT]]></text></news_body>
```

Литералы (D): `<news_body><id>` `0x180834568`, `</id><text><![CDATA[` `0x180834540`,
`]]></text></news_body>` `0x180834518`. В O-прогонах колбэк `<news_body>` не появился (см. I).

## Механика выдачи колбэков (D)

- `FUN_1800f7b40` (общий генератор XML) → `FUN_18011d740` → builders;
- заголовки: очередь `conn+0x248`, emit + очистка;
- тела: очередь `conn+0x260` (запись 0x18 Б: `L id`, `L len`, `ptr`); для каждого id
  ищется заголовок в сторе `conn+0x11850`; если нет — в лог пишется `Header with id = N not found`
  (`CallBackNews`) и элемент отбрасывается; если у заголовка есть флаг 0x80 — распаковка тела.

## O: команды и ошибки (прогон `.re-run/t49/news-final`)

| Команда | Результат DLL |
|---|---|
| `get_old_news count="2"` | success; C>S 73: `71 3e 91 3e 02 01` |
| `get_news_body news_id="2002"` | success; C>S 73: `21 7d 21 7d 01 02` |
| `get_old_news count="0"` | `News count must be in interval from 1 to 100. Received value is 0` |
| `get_old_news count="101"` | `News count must be in interval from 1 to 100. Received value is 101` |
| `get_old_news` | `"count" attribute not found` |
| `get_news_body` | `"news_id" attribute not found` |
| `get_news_body news_id="-1"` | `Attribute "news_id" must be in interval from 0 to 2147483647` |
| `get_news_body news_id="999999"` | `Header with id = 999999 not found` |

## Фикстуры (`native/records/testdata`)

| Файл | Что | Статус |
|---|---|---|
| `news-inject.hex` | 116 Б: `0x35` (id 1001, ts 1762300000, source TESTNEWSSOURCE, title "Test news title") + `0x36` flags=3 (id 2002, ts 1762300100, title "Upload news title", тело "Test news body text" как int32+объём + zlib) | O |
| `news-dll.xml` | два ожидаемых колбэка `<news_header>` (id 1001 и 2002) | O |
| `news-repro.md` | происхождение, обезличивание, воспроизведение | — |

## Task-56: reply-механика RQ_GETNEWS, 0x36-флаги, lastId (доработка)

### lastId стора = МИНИМАЛЬНЫЙ id (D + O)

- D: `FUN_1800c9a30(store)` возвращает `*(u32*)(*(longlong*)*store + 0x1c)` — id **первого узла**
  контейнера (std::map по id → минимальный id); пустой стор → `0xffffffff` → в запросе
  `from=0, to=0x7fffffff`.
- O (модифицированный dump, синтетические RQ_MAIN-кадры): заголовки `{1001}` → запрос
  `get_old_news count=2`: `L 999, L 1001, B 2, B 1`; `{1500}` только → `L 1498, L 1500`;
  `{1001,1500}` → `L 999, L 1001` (не 1498/1500!); `{1001,2002}` → `L 999, L 1001`.
  Т.е. «последний id» в DLL — минимальный известный, а не максимальный (наблюдаемое поведение
  DLL 6.47; для Go-реализации T24 это O-факт, можно повторять, но выглядит как особенность DLL).

### Тело новости: почему `<news_body>` не снялся (D + O)

- `get_news_body` лишь отправляет RQ_GETNEWS и возвращает success по факту отправки
  (`FUN_1800ed8b0` → `FUN_18035b9e0` → `FUN_1803b2d20`); разбор ответа 73 в самой команде нет.
- Поиск разборщика ответа 73: ни одна функция, кроме билдеров/команд, не читает стор новостей
  `conn+0x11850` и не пишет в очередь тел `conn+0x260`; общий отправитель
  `FUN_180355d50` — транспорт, а не разбор. Разбор ответа 73 — **I** (не найден).
- O-попытки получить `<news_body>` (loopback, модифицированный dump; синтетические S>C кадры):
  1. ответ 73 = package `int32 uncompressed + zlib`; 2. `+L id`; 3. `+L id +L len`;
  4. `+L len`; 5. `L count +L id +L len + package`; 6. `L count +L len + package`;
  7. запись `0x36` flags=2 (тело) в RQ_MAIN как отдельный кадр после заголовка.
  Во всех случаях команда success, но колбэка `<news_body>` нет — конверт ответа/условие
  выдачи тела **I**.
- D-механика очереди тел: `FUN_18011d740` → `FUN_1800c8c30` мерджит элементы очереди в стор;
  элемент с `byte+0x10 == 0` переносится в стор и удаляется из очереди (тело, пришедшее в
  RQ_MAIN 0x36, «тихо» попадает в стор — O-наблюдение: колбэка нет). Условие, при котором элемент
  остаётся в очереди и печатается `<news_body>` (`byte+0x10 != 0`), и установка флага 0x80
  заголовка (`FUN_1800c9b30` → `byte+0x20`, при 0x80 — распаковка тела `FUN_1800c95a0`) —
  **I**; вероятно, выставляется только reply-путём.

### 0x36 flags — подтверждено

- bit0 = заголовок, bit1 = тело (D-разборчик диспетчера; O: flags=1/3 → `<news_header>`,
  flags=2 → тело без колбэка).
- Инструментарий: для этих прогонов дамп модифицировался (синтетические S>C кадры type=5/73
  вставлялись в расшифрованный dump; скрипт `.re-run/t49/mk-news-dump.py`, вне git) — RE-3 без
  `-inject`, чтобы отделить RQ_MAIN-инъекцию от reply-инъекции.

## I: что не восстановлено

1. **Колбэк `<news_body>` оракулом не снят** (см. раздел Task-56): разборщик ответа
   RQ_GETNEWS не найден (обменник `FUN_1803b2d20` — только отправка), все опробованные конверты
   ответа 73 и RQ_MAIN 0x36-тела не дают колбэка; условие выдачи тела (`byte+0x10 != 0` в
   очереди) и установка флага 0x80 заголовка — **I**. Форма XML тела — D (полная).
2. Что означает `B flag` в 0x35 и как выставляется флаг 0x80 заголовка — **I**.
3. `lastId` стора — минимальный id (D+O, см. раздел Task-56); почему DLL использует
   минимальный, а не максимальный id, и как это влияет на выдачу старых новостей — **I**.
4. Время в `<timestamp>` — локальное время машины (O-наблюдение на одном сдвиге); для S-сверки
   нужен живой сервер (**I**).
5. **S-негатив:** в 9 записанных дампах RQ_MAIN записи 0x35/0x36 не встречаются (демо-новостей
   не было / сессии короткие); живой демо-сервер по указанию не использовался. S-фактов нет.

## Источники

D: `tools/re/ghidra/XrefDecomp.java` по `txmlconnector64-6.47.2.26.4.dll`; функции и адреса — выше.
O: `tools/replay-server` + `tools/tcproxy` + DLL под Wine 11.21, dump `.re-run/r14/decode-full.txt`
(вне git), инъекция RQ_MAIN после RQ_HISTORY_EXT (148); команды воспроизведения — `news-repro.md`.
