# testdata/sber-10m.jsonl.gz — синтетическая запись стакана

Обезличенная (полностью синтетическая) десятиминутная выгрузка `TQBR:SBER` в схеме полей
`md.book_events` / `md.trades` задачи 11. Живых данных, кредитов, номеров счетов/клиентов здесь нет:
`session_id` — заглушки `00000000-0000-4000-8000-000000000014` и `...15`, запись сгенерирована.

## Формат

JSONL: один объект в строке, строки двух таблиц вперемешку, порядок — по неубыванию `ts`.

| Поле | `book_events` | `trades` |
|---|---|---|
| `table` | `"book_events"` | `"trades"` |
| `ts`, `recv_ts` | текст ClickHouse `DateTime64(3)`/`DateTime64(6)`, UTC | то же |
| `session_id`, `seq` | UUID сессии рекордера и сквозной номер строки | — |
| `board`, `sec_code` | `TQBR` / `SBER` | то же |
| `kind` | `snapshot` / `level` / `gap` | — |
| `side` | `bid` / `ask` (у `gap` — `none`) | `buy` / `sell` (агрессор) |
| `price` | `Decimal64(6)` строкой, `"291.500000"` | то же |
| `qty` | объём уровня, 0 — удаление | количество |
| `trade_no`, `open_interest` | — | номер сделки и открытый интерес |

Цены — строки, чтобы ни в одном потребителе не появлялся `float64`. В середине записи
(07:05 UTC) — разрыв: строка `kind="gap"`, затем новая сессия и полный `snapshot` после
переподключения (Review Focus 3: replay не склеивает книгу до и после разрыва).

## Происхождение

Файл целиком сгенерирован `testdata/generate.go` (детерминированный LCG, seed `0x5eed202611050014`,
без часов и сети):

```bash
cd terminal/lobreplay
go run testdata/generate.go   # перезаписывает testdata/sber-10m.jsonl.gz байт в байт
```

Схема полей совпадает с DDL из `terminal/recorder/schema.go` и `docs/ai-terminal/data.md`.
