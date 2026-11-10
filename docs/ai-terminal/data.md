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

CREATE TABLE IF NOT EXISTS md.news (
    first_seen DateTime64(3, 'UTC'),
    published DateTime64(3, 'UTC'),
    news_id Int64,
    source LowCardinality(String),
    title String,
    body String,
    content_hash UInt64
) ENGINE = ReplacingMergeTree(first_seen)
ORDER BY (news_id);
```

Семантика полей:

| Поле | `md.book_events` | `md.trades` |
|---|---|---|
| `ts` | момент, когда рекордер увидел изменение книги, мс UTC; у записи R_QUOTES (0x07) **нет биржевого времени** на проводе — это ближайшая честная метка (**I**) | биржевое время сделки `model.Trade.Time` (Unix-время сервера + bias + миллисекунды из `<time>`), мс UTC (**S/D** для полей записи, см. [history.md](https://github.com/kmlebedev/txmlconnector-native/blob/main/docs/protocol/alltrades.md)) |
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
- **`md.news` (первый момент появления = point-in-time).** `first_seen` — момент, когда рекордер
  впервые получил новость; `published` — биржевой `timestamp` из заголовка (Unix-секунды на проводе,
  UTC). `first_seen` — версия `ReplacingMergeTree`: тело, пришедшее позже заголовка, пишется новой
  строкой того же `news_id` с **тем же** `first_seen` (поздняя версия побеждает при мердже, а
  point-in-time не сдвигается). `content_hash` — FNV-1a 64 от `source\x00title\x00body`; `source`/`title`
  из CP1251 уже переведены в UTF-8 нативным клиентом, `body` — распакованный текст.
- Retention/TTL у таблиц не задан — вопрос следующих задач (**I**).

## 3. Рекордер

```go
type Sink interface {
    Book([]BookEvent) error
    Trades([]TradeEvent) error
    News([]NewsEvent) error
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
  (**S-негатив**, см. [alltrades.md](https://github.com/kmlebedev/txmlconnector-native/blob/main/docs/protocol/alltrades.md)), поэтому трактовка «первый колбэк —
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
- **Новости (I, поведение клиента).** На проводе у новостей **нет подписки** (см.
  [news.md](https://github.com/kmlebedev/txmlconnector-native/blob/main/docs/protocol/news.md)): заголовки приходят записями RQ_MAIN только в ответ на
  `get_old_news`, тела — в ответ на `get_news_body`. Поэтому рекордер сам опрашивает их в
  горутине сессии: `OldNews` каждые 30 с (`defaultNewsPollInterval`) и `NewsBody` для каждого нового
  заголовка; данные приходят через колбэк `Run`, горутина только отправляет запросы. Ошибка опроса
  новостей — best effort и не останавливает запись стакана/сделок. `first_seen` фиксируется при
  первом заголовке и не меняется телом (тест `TestRecorderStoresNewsWithFirstSeen`); повторная
  выдача того же заголовка в `get_old_news` не пишется, исправленный заголовок пишется с прежним
  `first_seen` (`TestRecorderNewsHeaderChangeKeepsFirstSeen`).
- Ошибка `Sink` останавливает поток (потерянная вставка — это дыра в данных, а не повод продолжать
  молча); тест `TestRecorderPropagatesSinkError`.
- Тесты: `TestRecorderWritesSnapshotThenLevels`, `TestRecorderMarksGapOnReconnect`,
  `TestRecorderKeepsMillis`, `TestRecorderStoresNewsWithFirstSeen`,
  `TestRecorderNewsHeaderChangeKeepsFirstSeen`, `TestRunPollsOldNews`,
  `TestRunSubscribesReadOnlyFeeds`, `TestRunRejectsBadInput`, `TestSchemaStatements`,
  `TestEnsureSchemaCreatesTables`, `TestBookRowMapping`, `TestTradeRowMapping`,
  `TestNewsRowMapping`, `TestClickHouseSinkInserts`, `TestNewSessionIDIsUUIDv4` — все на
  `httptest`/фейках, без сети и без ClickHouse.

## 4. Новости в терминале и инструмент агента `get_news`

- **Лента терминала (feed).** Live-режим опрашивает заголовки `get_old_news` (раз в 30 с,
  `newsPollCount=100`) и публикует `feed.Event{Type:"news", Data: feed.News}` — `ID`,
  `Published` (время из заголовка, UTC), `FirstSeen` (момент получения фидом), `Source`, `Title`,
  `Body` (пусто, пока не пришло тело). Тело, пришедшее позже, обновляет кэш и публикует событие, но
  `FirstSeen` не меняет. Доступ — `feed.Service.News(since, limit)` (свежие первыми, кольцо на 200
  новостей). Offline-режим новостей не имеет (в seed их нет).
- **Инструмент агента `get_news(symbol?, since?, limit?, asOf)`** (Phase D, plan Task 12; с T18 —
  обязательный `asOf`) читает **`md.news`** через `chstore`, т.е. записанные рекордером новости с
  точным `first_seen`:
  - `asOf` — обязательный point-in-time (RFC3339 UTC, T18): новости с `first_seen` позже `asOf`
    не возвращаются, окно `since` отсчитывается от `asOf`, а не от текущего времени; без `asOf` —
    ошибка аргумента (см. [research.md](research.md) §8);
  - `since` — начало окна по `first_seen`: RFC3339, `ГГГГ-ММ-ДД`, `ГГГГ-ММ-ДД ЧЧ:ММ:СС` или
    длительность (`24h`); по умолчанию последние 24 часа **до `asOf`**;
  - `limit` — 1..500, по умолчанию 50; ответ модели компактный (`{asOf, since, count, news:[...]}`
    + `artifactId`), полный список — в артефакте `news`, от свежих к старым;
  - `symbol` — необязательный текстовый фильтр: на проводе новость не привязана к бумаге, поэтому
    фильтр ищет тикер в заголовке/тексте без учёта регистра
    (`positionCaseInsensitiveUTF8(concat(title,' ',body), ...)`).
  Код — [`terminal/agent/tools_news.go`](../../terminal/agent/tools_news.go): `NewsStore`,
  `ClickHouseNewsStore`, `get_news`-дескриптор и хук `invokeNewsTool`. Инструмент подключён:
  `newsToolCatalog()` добавлен в `buildTools()`, `toolNameGetNews` — в `toolCatalogNames`,
  `invokeTool()` вызывает `e.invokeNewsTool(ctx, e.news, ...)`, а store приходит из
  `agent.Config.NewsStore`; `cmd/terminal/wiring.go` строит `ClickHouseNewsStore` из
  `CLICKHOUSE_URL`/`MD_CLICKHOUSE_*` (без URL инструмент отвечает понятной ошибкой «recording is
  off»). Тесты инструмента: `TestGetNewsToolFiltersBySince`, `TestNewsToolCatalog`,
  `TestInvokeToolGetNewsDispatch`, `TestClickHouseNewsStoreQuery` (httptest), плюс
  `TestNewsStoreFromEnv` в `cmd/terminal`.
- **UI.** Новость приходит в SSE как `event: message`, `{"type":"news","data":{id,published,firstSeen,
  source,title,body?}}` (RFC3339 UTC): `terminal/api/sse.go onFeedEvent` пробрасывает
  `feed.Event{Type:"news"}` в hub (тест `TestFeedNewsForwarding`). Контракт зафиксирован в
  [ARCHITECTURE.md](ARCHITECTURE.md) §6 (тип `Event`) и §10.3 (тип `news` SSE).

## 5. Команда `cmd/md-recorder`

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

## 6. Проверки и что не проверено

Выполнено (unit, без сети):

```bash
export GOCACHE="$PWD/.cache/go-build"
go test -race ./terminal/chstore ./terminal/recorder ./terminal/feed ./terminal/vintage
go test -race ./terminal/agent -run 'News'
go build ./cmd/md-recorder
gofmt -l terminal/chstore terminal/recorder terminal/feed terminal/vintage terminal/agent/tools_news.go cmd/md-recorder
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
  см. [alltrades.md](https://github.com/kmlebedev/txmlconnector-native/blob/main/docs/protocol/alltrades.md)); в рекордере покрыто юнит-тестом.
- Многосессионность: `session_id` на сессию и `gap` при обрыве проверены юнит-тестами, не живым
  переподключением.
- **Живой поток новостей не снимался**: `get_old_news`/`get_news_body` к демо-серверу в этой
  задаче не вызывались (нет согласия пользователя на живую сессию); формат заголовков/тел взят из
  [news.md](https://github.com/kmlebedev/txmlconnector-native/blob/main/docs/protocol/news.md) (**S/D/O**), а семантика `first_seen` — контракт рекордера (**I**).
  `get_news` против реального ClickHouse не гонялся: SQL и разбор ответа проверены на
  `httptest`-заглушке.

## 7. Винтажные (point-in-time) данные: макро, календарь, новости

Пакет [`terminal/vintage`](../../terminal/vintage/vintage.go) — слой доступа исследовательских задач
(фазы E–F) к **read-only** витринам проекта `clickhouse-import-rosstat` через `terminal/chstore`.
Никаких новых зависимостей: только `net/http`; все моменты времени — UTC.

```go
type Store interface {
    AsOf(ctx context.Context, series string, asOf time.Time) ([]Point, error)
    Releases(ctx context.Context, from, to time.Time, categories ...string) ([]Release, error)
    News(ctx context.Context, from, to time.Time, sources ...string) ([]NewsItem, error)
}
type Point struct { Series string; Ref time.Time; Value float64; PublishedAt time.Time; Vintage string }
type Release struct { At time.Time; Category, Title string; Country string; Exact bool }
type NewsItem struct { At time.Time; Source, Title string; Sentiment float64; Tags []string }
```

- **Правило as-of — строгое:** `AsOf` возвращает версию, опубликованную **до** `asOf`
  (`published_at < asOf`); значение, опубликованное ровно в `asOf`, в срез не входит. Три пути
  (в порядке приоритета, [`vintage.go`](../../terminal/vintage/vintage.go)):
  1. параметризованная витрина `v_point_in_time_asof(as_of='YYYY-MM-DD')` — винтажные релизы
     `cbr_dip_bulletin` (например `unemployment_sa`): внутри витрины `argMax(value, (published_at,
     vintage))` по каждому отчётному периоду. Аргумент витрины — SQL-литерал, поэтому дата
     форматируется кодом и никогда не приходит из пользовательского текста; серия связывается
     через `{series:String}`;
  2. join `macro_series × series_publication` — ряды с публикационными датами
     (`published_at DateTime('Europe/Moscow')`, конвертируется в UTC в SQL); граница строгая;
  3. `macro_series` без винтажного слоя — последние известные значения, помеченные
     `Vintage = vintage.VintageLatestOnly`; это **не** as-of срез, потребитель обязан выдать
     `vintage.LatestOnlyWarning(series)` и может отказаться от ряда (Review Focus 1/14).
- **`Point.Ref`** — отчётный период значения (UTC-полночь даты источника), `PublishedAt` — момент,
  когда версия стала публичной (UTC), `Vintage` — id версии источника (или `latest-only`).
- **`Releases`** читает `v_events_calendar` (триггеры FOMC/CPI/NFP/ЦБ/EIA/…) и
  `v_cbr_publication_calendar` (план публикаций ЦБ). Время в источниках — **местное МСК**:
  `At` нормализуется в UTC (13:30 МСК → 10:30 UTC); строка только с датой получает `Exact=false`
  и полночь МСК. Категории приводятся к контракту (`cbr`→`cbr_rate`, `cpi`→`us_cpi`,
  `nfp`→`us_nfp`, `fomc`→`fomc`; публикации ЦБ — по теме/заголовку: ключевая ставка→`cbr_rate`,
  инфляция→`ru_cpi`, ВВП→`ru_gdp`), `Country` — `RU`/`US`, фильтр `categories` применяется
  после маппинга; окно `[from, to]` — по UTC-инстантам.
- **`News`** читает `v_news_events` (витрина уже join'ит `news_annotation`): `At` из UTC-нормализованного
  `date`, `Sentiment` — LLM-тональность −1..1 (0 = разметки нет, NULL в источнике), `Tags` —
  `"dimension:tag:direction"` из `Array(Tuple)`. Окно — `[from, to]` UTC, `sources` — фильтр
  по ленте (`interfax`, `rbc`, …) через `param_srcN`-биндинги.
- **Wiring:** `cmd/terminal/wiring.go` читает read-only ClickHouse из `CLICKHOUSE_URL`
  (`MD_CLICKHOUSE_URL` важнее; креды `CLICKHOUSE_USER/PASSWORD` / `MD_CLICKHOUSE_*`) и отдаёт
  `vintageStoreFromEnv() vintage.Store` (nil без endpoint: терминал работает, исследовательские
  данные недоступны). Тот же `chstoreFromEnv()` создаёт клиента для `get_news` (md.news).
- **Тесты** (httptest-заглушка ClickHouse, без сети): `TestVintageAsOfExcludesLaterRevision`
  (Review Focus 1: два релиза `unemployment_sa` за месяц — на дату между релизами первая версия,
  на позднюю — ревизия, на дату публикации — пусто из-за строгой границы),
  `TestVintageSeriesPublicationPath`, `TestVintageLatestOnlyFlagged`,
  `TestReleasesNormalizedToUTC` (13:30 МСК → 10:30 UTC, date-only → полночь МСК и `Exact=false`),
  `TestReleasesReadsEventsCalendar`, `TestNewsFiltersAndDecodes`; `TestVintageStoreFromEnv` в
  `cmd/terminal`.

**Не проверено (I):** живого ClickHouse нет (нет согласия пользователя на подъём/запись), поэтому
SQL и его аргументы проверены только на httptest-заглушках; схемы витрин
(`v_point_in_time_asof`, `series_publication`, `v_events_calendar`, `cbr_publication_calendar`,
`v_news_events`, `news_annotation`) взяты из соседнего проекта `clickhouse-import-rosstat`
(код + ARCHITECTURE §6), а не из живого сервера — расхождение с фактической базой возможно и
выяснится при первом живом прогоне. Имена рядов/категорий в реальных данных не сверялись.


## 8. Живая проверка SQL на dev-витринах clickhouse-import-rosstat (S, 2026-11-06)

Источник: локальный dev-ClickHouse проекта `clickhouse-import-rosstat` (server 26.10.1, HTTP
`127.0.0.1:8123`); его же MCP-конфиг (`.kimi-code/mcp.json` проекта) указывает на эту БД (сервер
`clickhouse`, stdio, витрины `v_*`). Проверка выполнена с согласия пользователя, только `SELECT`.

Что проверено:

- Все объекты, которые использует `terminal/vintage`, существуют и отвечают: `v_point_in_time_asof`
  (View, вход `as_of = 'YYYY-MM-DD'`), `series_publication` (DateTime `Europe/Moscow`),
  `v_events_calendar`, `v_cbr_publication_calendar`, `v_news_events`, `news_annotation`.
- `AsOf("unemployment_sa", 2024-02-07)` → 107 точек, последняя 2023-11-30 = 2.885274, опубликована
  2024-02-06 (строгая граница `published_at < asOf`) — Review Focus 1 подтверждён на живых данных.
- `Releases(2026-10-01 .. 2027-01-01)` → 260 событий; `News(2026-10-01 .. 2026-11-06)` → 773 новости;
  nullable-тональность и теги `dimension:tag:direction` декодируются (sentiment NULL → 0 = «не размечено»).
- Живые интеграционные тесты: `TXML_IT_CLICKHOUSE=1 TXML_CLICKHOUSE_URL=http://localhost:8123
  go test ./terminal/vintage ./terminal/volatility -run Live -v` (без env — `t.Skip`, сеть в unit-тестах
  по-прежнему запрещена). Файлы: `terminal/vintage/clickhouse_live_test.go`,
  `terminal/volatility/clickhouse_live_test.go`.

Найдено и исправлено по живым данным:

- `v_events_calendar.category = 'cpi'` смешивает недельный ИПЦ Росстата и CPI США. Маппинг по одной
  категории ошибался; теперь категория уточняется по заголовку: «недельный ИПЦ/Росстат» → `ru_cpi`/RU,
  «CPI США» → `us_cpi`/US (unit-тест `TestReleasesCPICategoryDisambiguation` + live-проверка).
- `v_point_in_time_asof.published_at` — тип `Date` (не DateTime): `PublishedAt` точки = полночь UTC
  даты публикации; это задокументировано, для point-in-time семантики достаточно.

Остаточные ограничения (I):

- `event_time` в `v_events_calendar` у US-релизов зимой выглядит как фиксированные ET+7 (например,
  «CPI США…» 2026-12-10 15:30 MSK), а не ET с переходом на зимнее время (ожидалось 16:30 MSK).
  Точные времена US-релизов — задача Phase X2 (автоматизация календаря FOMC/BLS); до неё окна
  релизов в оракуле волатильности для зимних US-событий могут быть смещены на час.
- `us_alfred_vintage`/ALFRED-винтажей в dev-БД нет (в соседнем проекте только актуальные ряды FRED:
  `v_fred_macro`) — Phase X1 остаётся внешней задачей.

### 8.1. Схема записи md (T11) на живом ClickHouse (S)

**Живой прогон 2026-11-06 (демо-окно 09:55–10:05 MSK, `tools/demo-live-check.sh`, S):**

- `md-recorder` 10 минут по `TQBR:SBER` против `tr1-demo5.finam.ru:3939`;
- `SELECT kind, count() AS n FROM md.book_events GROUP BY kind`: `snapshot`=120, `level`=5161, `gap`=1;
- `md.trades` = 49 394, `md.news` = 2 (`first_seen` от 2025-11-04); ClickHouse — локальный dev
  `127.0.0.1:8123`;
- итог T11 — **PASS**; журнал: `.re-run/live/demo-check-20261106-103039.log`,
  сводка: `.re-run/live/live-summary-20261106-103039.json`.

`recorder.EnsureSchema` прогнан на живом ClickHouse: созданы `md.book_events`, `md.trades`, `md.news`;
типы колонок совпадают с планом (`ts DateTime64(3,'UTC')`, `recv_ts DateTime64(6,'UTC')`,
`kind Enum8(...)`, `price Decimal(18, 6)`, `qty Int64`). Живая запись стакана/сделок и `SELECT
kind,count()` — выполняются отдельной проверкой в окне демо-сервера (T11 Step 5), результат — в
PROGRESS.md.
**Пайплайн T11 проверен end-to-end локально (S, 2026-11-06):** реальный `native.Client` говорит с
серверной тестовой заглушкой `native/testserver` (настоящий протокол TRANSAQ), события стакана/сделок
декодируются клиентом, проходят через `recorder.Run` и пишутся в живой ClickHouse
(`md.book_events`, `md.trades`). Тест `TestLiveRecorderWritesBookAndTradesToClickHouse`
(`terminal/recorder/clickhouse_live_test.go`, env-гейт `TXML_IT_CLICKHOUSE=1`) — PASS: строки
`snapshot`/`level` для `TQBR:SBER`, `Decimal`-цены, сделки с миллисекундами. Запись с реального
демо-сервера (T11 Step 5) остаётся за фоновой приёмкой в окне. Для этого сценария `native/testserver`
научился отвечать на `RQ_GET_NEWS` пустым списком (recorder опрашивает новости в Run).
**T12 (news → `md.news`) проверен end-to-end локально (S, 2026-11-06):** тот же тест
`TestLiveRecorderWritesBookAndTradesToClickHouse` подаёт в поток реальные R_NEWS-заголовки
(`news-inject.hex`, обезличенные `TESTNEWSSOURCE`/`Test news title`), recorder пишет строку в
`md.news` с `FirstSeen` от собственных часов (строго после старта теста), `Published` из заголовка,
source/title непустые. Живой news-поток с демо-сервера (HTTP-проверка `md.news.first_seen`) остаётся
за фоновой приёмкой в окне.
Живая проверка второго источника календаря (`v_cbr_publication_calendar`, mapping по заголовкам):
«Резюме обсуждения ключевой ставки» → `cbr_rate`/RU, «Инфляция в России» → `ru_cpi`/RU; в окне
2026-10-01…2027-01-01 — 1 и 2 события соответственно (плюс 12 «Росстат: недельный ИПЦ» и 3 «CPI США»
из `v_events_calendar`). Проверяется в `TestLiveClickHouseReleases`.
