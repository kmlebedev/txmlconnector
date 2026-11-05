# Данные AI-терминала: ClickHouse `md` (сделки и стакан)

Дата: 2026-11-06. Ветка: `plan/ai-terminal-a`. Задача плана: **Task 11** (Phase D).
Статусы фактов: **S** — проверено на демо-сервере, **O** — оракул DLL, **D** — декомпилятор,
**I** — вывод/не проверено. Живая запись в ClickHouse (Step 5 плана) **не выполнялась** — нет явного
согласия пользователя на запись; всё, что зависит от живого прогона, помечено **I**.

Пакеты: [`terminal/chstore`](../../terminal/chstore/client.go) — HTTP-клиент ClickHouse на stdlib;
[`terminal/recorder`](../../terminal/recorder/recorder.go) — конвертер событий `native.Client` в
строки `md.book_events`/`md.trades` и DDL; [`cmd/md-recorder`](../../cmd/md-recorder/main.go) —
демон записи. Новых зависимостей нет: ClickHouse — через `net/http`, никакого драйвера.

## 1. Клиент `chstore`

```go
func New(baseURL, user, password string, hc *http.Client) *Client
func (c *Client) Insert(ctx context.Context, table string, rows any) error   // INSERT … FORMAT JSONEachRow
func (c *Client) Query(ctx context.Context, sql string, params map[string]string, dst any) error
func (c *Client) Exec(ctx context.Context, sql string) error
```

- **Аутентификация — заголовками** `X-ClickHouse-User`/`X-ClickHouse-Key`, а не в URL: строка
  запроса не содержит секретов, и её можно логировать. Ошибки ClickHouse обрезаются до 8 КиБ
  (`chstore.Error` с `StatusCode` и телом исключения), пароль в текст ошибки не попадает.
- `Insert` режет срез на батчи по 10 000 строк, каждая строка — JSON-объект JSONEachRow
  (проверено: 10 001 строка → 2 запроса 10000 + 1). Пустой срез не отправляет запрос; имя таблицы
  валидируется как идентификатор `table`/`database.table` (защита от подстановки SQL).
- `Query` дописывает `FORMAT JSONEachRow` (если его ещё нет), параметры передаются как
  `param_<name>` для плейсхолдеров `{name:Type}`, ответ «по строке на объект» превращается в
  JSON-массив и декодируется в `dst` (nil — строки отбрасываются).
- `Exec` — DDL без разбора результата; хвостовая `;` снимается (ClickHouse HTTP её не принимает).
- Пер-запросный таймаут по умолчанию 60 с, ответ ограничен 64 МиБ.
- Тесты (`httptest`, без сети): `TestChstoreInsertBatchesJSONEachRow`,
  `TestChstoreQueryParams`, `TestChstoreExec`, `TestChstoreReportsHTTPError`,
  `TestChstoreRejectsInvalidBaseURL`.
- **Не проверено (I):** живой ClickHouse в задаче не поднимался. Типы `DateTime64`/`Decimal64`/
  `Enum8` вставляются строками, и это проверено только юнит-тестами на уровне сформированного
  JSON, а не реальной вставкой в базу.

## 2. Схемы базы `md` (DDL)

Создаются `recorder.EnsureSchema`: **сначала `CREATE DATABASE IF NOT EXISTS md`**, затем таблицы
(`CREATE TABLE IF NOT EXISTS`), — на чистом ClickHouse базы `md` нет, и `CREATE TABLE md.*` без
этого шага упал бы с «Database md does not exist». Точный DDL — в
[`recorder/schema.go`](../../terminal/recorder/schema.go).

```sql
CREATE TABLE IF NOT EXISTS md.book_events (
    ts DateTime64(3, 'UTC'),
    recv_ts DateTime64(6, 'UTC'),
    session_id UUID,
    seq UInt64,
    board LowCardinality(String),
    sec_code LowCardinality(String),
    kind Enum8('snapshot' = 1, 'level' = 2, 'gap' = 3),
    side Enum8('bid' = 1, 'ask' = 2, 'none' = 0),
    price Decimal64(6),
    qty Int64
) ENGINE = MergeTree
ORDER BY (sec_code, ts, seq)
PARTITION BY toYYYYMMDD(ts);

CREATE TABLE IF NOT EXISTS md.trades (
    ts DateTime64(3, 'UTC'),
    recv_ts DateTime64(6, 'UTC'),
    board LowCardinality(String),
    sec_code LowCardinality(String),
    trade_no Int64,
    price Decimal64(6),
    qty Int64,
    side Enum8('buy' = 1, 'sell' = 2, 'none' = 0),
    open_interest Int64
) ENGINE = ReplacingMergeTree
ORDER BY (sec_code, trade_no);
```

Семантика полей:

| Поле | `md.book_events` | `md.trades` |
|---|---|---|
| `ts` | момент, когда рекордер увидел изменение книги, мс UTC; у записи R_QUOTES (0x07) **нет биржевого времени** на проводе — это ближайшая честная метка (**I**) | биржевое время сделки `model.Trade.Time` (Unix-время сервера + bias + миллисекунды из `<time>`), мс UTC (**S/D** для полей записи, см. [history.md](../protocol/alltrades.md)) |
| `recv_ts` | точный момент приёма события рекордером, мкс UTC | то же для сделки |
| `session_id` | UUIDv4 на каждый запуск `recorder.Run` (переподключение с новым полным логином = новая сессия) | — (идентичность сделки задаёт `trade_no`) |
| `seq` | сквозной номер строки в сессии — детерминированно упорядочивает строки, попавшие в одну миллисекунду | — |
| `board`, `sec_code` | стабильный ключ бумаги (`TQBR`/`SBER`), не session-local secid | то же |
| `kind` | `snapshot` — полный снимок после (пере)подписки; `level` — изменение уровня; `gap` — маркер разрыва записи | — |
| `side` | `bid`/`ask` уровня; у `gap` — `none` | `buy`/`sell` из `<buysell>`; `none`, если поля не было |
| `price` | цена уровня, `Decimal64(6)`; удалённый уровень сохраняет свою цену | цена сделки, `Decimal64(6)` |
| `qty` | объём уровня; удаление уровня (`Buy/Sell = -1` в PDF 4.9) пишется как `qty = 0` с тем же `side`/`price` | количество |
| `open_interest` | — | открытый интерес, 0 если запись его не несла |

- **Цены не проходят через `float64`:** `model.Decimal` (`Units`/`Scale`) рендерится ровно в 6
  знаков строкой (`29146` × `10^-2` → `"291.460000"`), `Decimal64(6)` парсит её без потерь.
  Значение с более чем 6 знаками усекается (для бумаг MOEX не встречается — **I**).
- `md.trades` — `ReplacingMergeTree ORDER BY (sec_code, trade_no)`: повторная запись той же сделки
  (переподключение/переигровка) схлопывается при мердже.
- Retention/TTL у таблиц не задан — вопрос следующих задач (**I**).

## 3. Рекордер

```go
type Sink interface {
    Book([]BookEvent) error
    Trades([]TradeEvent) error
}
func Run(ctx context.Context, cl *native.Client, sink Sink, symbols []string) error
```

- `symbols` — `BOARD:SECCODE`; на них оформляются **только read-only подписки** `<quotes>` и
  `<alltrades>` (`native.Subscription{Quotes, AllTrades}`). Поток `<quotations>` не подписывается,
  торговых команд рекордер не отправляет вовсе.
- **Снимок стакана после каждой (пере)подписки (I, поведение клиента).** Первый `<quotes>` колбэк
  после подписки — полный снимок книги и пишется строками `kind='snapshot'`; последующие колбэки —
  изменения уровней `kind='level'`. Удаление уровня (`buy/sell = -1`) → `qty=0`. На проводе
  R_QUOTES приходит **полным снимком** при подписке; инкрементальных обновлений живьём не наблюдалось
  (**S-негатив**, см. [alltrades.md](../protocol/alltrades.md)), поэтому трактовка «первый колбэк —
  snapshot, далее level» — поведение рекордера (**I**), а не подтверждённый формат потока.
- **Разрыв записи (Review Focus 3).** При потере связи DLL отдаёт `server_status recover="true"`
  (`model.ServerStatus{Recover: true}`) — рекордер пишет по одной строке `kind='gap'`
  (`side='none'`, `price=0`, `qty=0`) на каждый символ. После успешного восстановления
  (`model.ServerStatus{Connected: true}`) рекордер **переподписывается**, чтобы получить новый снимок:
  следующий `<quotes>` снова пишется как `snapshot`. Replay (Task 14) обязан сбрасывать книгу на
  `gap` и не склеивать уровни до и после него. Тест — `TestRecorderMarksGapOnReconnect`.
- **Миллисекунды сделки.** `model.Trade.Time` уже содержит миллисекунды из `<time>`
  (`Catalog.Trade` прибавляет `Millis`), поэтому `ts` сделки сохраняет их без потерь; тест
  `TestRecorderKeepsMillis` проверяет 123 мс в `ts`.
- Если сессия восстановиться не смогла, `Run` возвращает ошибку, а `cmd/md-recorder` открывает
  новую сессию (новый `session_id`) — разрыв уже отмечен строкой `gap` старой сессии.
- Ошибка `Sink` останавливает поток (потерянная вставка — это дыра в данных, а не повод продолжать
  молча); тест `TestRecorderPropagatesSinkError`.
- Тесты: `TestRecorderWritesSnapshotThenLevels`, `TestRecorderMarksGapOnReconnect`,
  `TestRecorderKeepsMillis`, `TestRunSubscribesReadOnlyFeeds`, `TestRunRejectsBadInput`,
  `TestSchemaStatements`, `TestEnsureSchemaCreatesTables`, `TestBookRowMapping`,
  `TestTradeRowMapping`, `TestClickHouseSinkInserts`, `TestNewSessionIDIsUUIDv4` — все на
  `httptest`/фейках, без сети и без ClickHouse.

## 4. Команда `cmd/md-recorder`

| Переменная | Смысл |
|---|---|
| `TC_HOST`, `TC_PORT` | адрес биржевого сервера (обязательно) |
| `TC_LOGIN`, `TC_PASSWORD` | креды биржи (обязательно; никогда не логируются) |
| `MD_CLICKHOUSE_URL` | HTTP-endpoint ClickHouse, например `http://127.0.0.1:8123` (обязательно) |
| `MD_CLICKHOUSE_USER` | пользователь ClickHouse; fallback `CLICKHOUSE_USER` |
| `MD_CLICKHOUSE_PASSWORD` | пароль ClickHouse; fallback `CLICKHOUSE_PASSWORD` |
| `MD_SYMBOLS` | `TQBR:SBER,TQBR:GAZP`; по умолчанию `TQBR:SBER` |

```bash
export GOCACHE="$PWD/.cache/go-build"
set -a; . ~/re/demo.env; set +a        # TC_LOGIN/TC_PASSWORD (значения не печатать)
export MD_CLICKHOUSE_URL=http://127.0.0.1:8123
export MD_CLICKHOUSE_USER=md_writer MD_CLICKHOUSE_PASSWORD='***'
export MD_SYMBOLS=TQBR:SBER,TQBR:GAZP
go run ./cmd/md-recorder
```

Порядок работы: `EnsureSchema` (создаёт базу `md` и таблицы) → цикл сессий `native.Dial` →
`recorder.Run`. При обрыве сессия
переоткрывается с backoff 5 с → 1 мин (сброс после 5 минут стабильной работы); `SIGINT`/`SIGTERM`
завершают запись штатно. В лог пишутся только режим, адрес, число символов и класс ошибки — креды и
значения переменных не печатаются.

## 5. Проверки и что не проверено

Выполнено (unit, без сети):

```bash
export GOCACHE="$PWD/.cache/go-build"
go test -race ./terminal/chstore ./terminal/recorder
go build ./cmd/md-recorder
gofmt -l terminal/chstore terminal/recorder cmd/md-recorder
go vet ./... && go build ./...
make verify
```

**Не проверено (I), честно:**

- **Живая запись (Step 5 плана) не выполнялась.** Нет явного согласия пользователя на запись в
  ClickHouse и на живую сессию демо-сервера; запрос
  `SELECT kind, count() FROM md.book_events GROUP BY kind` (ожидаются `snapshot` и `level`)
  не выполнялся. Факт записи и наличие строк — **не подтверждены**.
- Реальный ClickHouse: DDL, вставка `Decimal64(6)`/`DateTime64(3|6)`/`Enum8` строками и
  `param_<name>` проверены только на `httptest`-заглушках; сервер ClickHouse не поднимался.
- Инкрементальные обновления стакана на проводе живьём не наблюдались (только полный снимок при
  подписке — **S-негатив**); раскладка «snapshot/level» — контракт рекордера (**I**).
- Удаление уровня (`-1`) на живом потоке не наблюдалось (поведение из PDF 4.9/DLL — **D**,
  см. [alltrades.md](../protocol/alltrades.md)); в рекордере покрыто юнит-тестом.
- Многосессионность: `session_id` на сессию и `gap` при обрыве проверены юнит-тестами, не живым
  переподключением.
