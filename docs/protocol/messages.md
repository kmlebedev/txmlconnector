# Текстовые сообщения: R_MSG (0x01) и <messages> (Task 25)

Дата: 2026-11-05. Источник: RE-1 (декомпилятор DLL 6.47.2.26.4) + RE-3 (оракул: replay-server +
DLL под Wine, только loopback). Статусы: **S** — демо-сервер, **O** — оракул DLL, **D** —
декомпилятор, **I** — вывод. Живой демо-сервер по указанию не использовался.

## Запись RQ_MAIN 0x01 R_MSG (без размера) — D + O

Диспетчер `FUN_18038d7d0` (case tag-1 = 0), поля читаются подряд:

```
B     flags            // bit0 и bit8 -> urgent (см. ниже); прочие биты — I
Z20   from             // C-строка до 20 символов (переменной длины, без добивки!)
L     date             // packed L: Unix-секунды «настенного» времени сервера
Z1024 text             // C-строка до 1024 символов (CDATA, переменной длины)
```

- `FUN_1801bd290` (B), `FUN_1801bd170` (Zn), `FUN_1801bd7b0` (packed L) — примитивы из
  `tools/re/README.md`.
- Ошибка чтения (D-строка): `RQ_MAIN R_MSG: return incorrect buffer`.
- Обработчик `FUN_18026c410`: кладёт сообщение в очередь `conn+0x18+0x5a8` (элементы 0x30 Б),
  возвращает число элементов-1; событие уходит в очередь `conn+0x70`.
- Дата перед постановкой в очередь нормализуется: `date % 86400` + пересчёт календарной даты
  через торговый календарь (`FUN_1801a22f0`, `local_252`) — **D** (точная семантика u16 — I).

## XML-форма (D-литералы + O)

Строитель `FUN_1800ff5a0`; сообщения батчатся в один колбэк на RQ_MAIN-ответ:

```xml
<messages>
  <message><date>HH:MM:SS</date><urgent>Y|N</urgent><from>FROM</from><text><![CDATA[TEXT]]></text></message>
  ...
</messages>
```

Литералы (D): `<messages>` `0x180835470`, `</messages>` `0x180835480`, `<message>`
`0x1808353b0`, `</message>` `0x180835460`, `<date>` `0x1808353bc`, `</date>`
`0x1808353c8`, `<urgent>` `0x1808353d0`, `</urgent>` `0x1808353f0`, `<from>`
`0x180835418`, `</from>` `0x180835420`, `<text><![CDATA[` `0x180835428`,
`]]></text>` `0x180835450`. Колбэк отправляется только если в очереди есть сообщения.

- `<urgent>`: `Y`, если `flags & 1` или `flags & 8`, иначе `N` (D: `FUN_180214690` →
  `FUN_1802146e0`; O: flags 0x01 и 0x08 → Y, 0x00/0x02 → N).
- `<date>` — **только время** `HH:MM:SS`, без даты: строитель вызывает
  `FUN_180052ca0(0, time, 0,0)` с date-mode 0 (D); O-значения совпадают с **WALL-временем**
  серверной записи (`1762300000` → `23:46:40`, `1762200000` → `20:00:00`), т.е. DLL при
  рендере печатает wall-календарь, а не UTC-instant. Локальная конверсия через RQ_TIME-пояс
  (conn+0x680) — **I** (в O-прогонах она не изменила вывод). Нативная модель хранит и UTC
  instant (Time = wall + bias), и WallTime для `<date>`, поэтому рендер не зависит от bias.
- `<from>`/`<text>` — как в записи (CDATA для текста; экранирование `]]>` внутри текста —
  **I**, DLL, судя по коду, не экранирует).

## O-прогоны

| Вход (инъекция 0x01) | Ответ DLL |
|---|---|
| flags=0, from TESTUSER, ts=1762300000, text "Test message text" + flags=1, from TESTURGENT, ts=1762300100, text "Urgent message text" | `<messages><message><date>23:46:40</date><urgent>N</urgent><from>TESTUSER</from><text><![CDATA[Test message text]]></text></message><message><date>23:48:20</date><urgent>Y</urgent><from>TESTURGENT</from><text><![CDATA[Urgent message text]]></text></message></messages>` |
| flags=8 | `<urgent>Y</urgent>` |
| flags=2 | `<urgent>N</urgent>` |
| несколько записей в одном RQ_MAIN-ответе | один колбэк `<messages>` с несколькими `<message>` (порядок записей) |

## Фикстуры (`native/records/testdata`)

| Файл | Что | Статус |
|---|---|---|
| `message-inject.hex` | 72 Б: две записи `0x01` (flags=0/from TESTUSER/ts 1762300000/"Test message text"; flags=1/from TESTURGENT/ts 1762300100/"Urgent message text") | O |
| `message-dll.xml` | ожидаемый колбэк `<messages>…</messages>` (две `<message>`) | O |
| `message-repro.md` | происхождение, обезличивание, воспроизведение | — |

## Реализация в Go (для задачи 25)

`model.Message`: `Date`/время (HH:MM:SS локальное; в модели — `time.Time` UTC по правилам
репозитория, при рендере — `Format("15:04:05")`), `Urgent bool`, `From string`, `Text string`;
события батчатся по RQ_MAIN-ответу. Тест `TestMessagesMatchDLL` — вход
`message-inject.hex`, рендер побайтно против `message-dll.xml`.

## I: что не восстановлено

1. Точная семантика `u16` в нормализованной дате (`local_252`) и байта +0x28 структуры; зачем
   нужен календарный пересчёт в записи (время в XML — только HH:MM:SS) — **I**.
2. Биты `flags` кроме 0x01/0x02/0x08 (например, «прочитано»/тип сообщения) — не проверялись.
3. Поведение при `from` длиной ровно 20 символов и `text` длиной ровно 1024 (обрезка/ошибка) —
   не проверялось.
4. Экранирование `]]>` в тексте (CDATA) и служебных символов XML — не проверялось; по коду
   выглядит как «вставка как есть» → риск сломать XML (I).
5. Часовой пояс: пересчёт «настенное серверное → локальное машины» подтверждён только на одном
   сдвиге (+5); на демо-сервере (другой TZ) — **I**.
6. **S-негатив:** в 9 записанных дампах записей `0x01` R_MSG не встречалось (живой демо не
   использовался по указанию) — S-фактов нет.

## Источники

D: `tools/re/ghidra/XrefDecomp.java` по `txmlconnector64-6.47.2.26.4.dll`; функции и адреса — выше.
O: `tools/replay-server` + `tools/tcproxy` + DLL под Wine 11.21, dump `.re-run/r14/decode-full.txt`
(вне git), инъекция RQ_MAIN после RQ_HISTORY_EXT (148); воспроизведение — `message-repro.md`.
