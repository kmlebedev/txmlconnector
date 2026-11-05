# AI-терминал Finam/MOEX: архитектура и контракты (итерация 1)

Дата: 2026-11-05. Ветка: `feat/ai-terminal`. Владелец: Lead. Это **единственный источник
контрактов** для команды, работающей над терминалом; пакеты и JSON-схемы ниже заморожены до
интеграции. Если контракт невозможно выполнить — сообщить Lead, не менять его молча.

## 1. Что строим

Нативное Go-приложение `cmd/terminal` (один бинарник, без DLL/Wine/CGO) поверх
`github.com/kmlebedev/txmlconnector/native`:

1. **UI-аналог терминала Finam** (`https://trading.finam.ru`): список инструментов со спарклайнами,
   свечной график с объёмом, стакан, тикер-лента, портфель/заявки/история, тикет покупки/продажи,
   статус-бар. Светлая тема, жёлтый акцент, плотные таблицы — см. §11 и референс-скриншот
   `.re-run/design/finam-reference.webp` (в git не входит, доступен всем агентам в общем дереве).
2. **AI-агент без MCP**: встроенный в бэкенд tool-calling loop (OpenAI-compatible LLM, настраивается
   через env) **или** детерминированный локальный аналитик, если LLM не настроен. Агент напрямую
   вызывает Go-инструменты терминала: реальная история котировок из native-клиента, бэктест,
   технические подсказки, стакан, портфель, бумажные заявки.
3. **Автономная торговая стратегия** на бумажном счёте: агент выбирает стратегию по бэктесту и
   запускает replay-автоторговлю по историческим барам с риск-лимитами (без реальных ордеров).
4. **Офлайн-демо на реальной истории**: снапшот демо-сервера TRANSAQ (свечи 1м/5м/15м/1ч/1д,
   котировки, стаканы) в `terminal/seeddata/data/seed.json.gz`; бинарник запускается без сети и
   секретов.
5. **Живой read-only режим**: подключение к TRANSAQ/Finam через `native.Client` (env
   `TRANSAQ_ADDR/TRANSAQ_LOGIN/TRANSAQ_PASSWORD`, fallback `TC_HOST:TC_PORT`/`TC_LOGIN`/`TC_PASSWORD`),
   реальные котировки/стаканы/история. Реальная торговля выключена по умолчанию и включается
   только флагами/env + подтверждением в запросе (§10).

Не входит в итерацию 1 (честно документируется как roadmap): реальная маршрутизация ордеров на
проде, T+1/маржинальная модель, опционы/фьючерсы в UI, live-автоторговля по стакану (автономия в
итерации 1 работает по историческим барам), мобильная вёрстка, свечи тиков.

## 2. Режимы, флаги, переменные окружения

`cmd/terminal` (все флаги — `flag`, без внешних зависимостей):

| Флаг | По умолчанию | Смысл |
|---|---|---|
| `-addr` | `127.0.0.1:8090` | адрес HTTP-сервера (только loopback по умолчанию) |
| `-mode` | `auto` | `auto` / `offline` / `live`; `auto` = live, если заданы адрес+логин+пароль, иначе offline |
| `-seed` | встроенный | путь к `seed.json.gz` (для отладки/разработки) |
| `-paper-cash` | `1000000` | стартовый кэш бумажного счёта, ₽ |
| `-fee-bps` | `5` | комиссия бумажных сделок, б.п. (0.05%) |
| `-llm` | из env | `off` принудительно выключает LLM |
| `-live-trading` | `false` | разрешить реальные заявки (дополнительно к env) |
| `-version` | — | печать версии и выход |

Env:

| Переменная | Смысл |
|---|---|
| `TRANSAQ_ADDR` | `host:port` биржевого сервера (live) |
| `TRANSAQ_LOGIN`, `TRANSAQ_PASSWORD` | секреты биржи; fallback `TC_LOGIN`/`TC_PASSWORD` |
| `TC_HOST`, `TC_PORT` | fallback-адрес, если `TRANSAQ_ADDR` пуст |
| `TERMINAL_LLM_BASE_URL` | OpenAI-compatible base URL, например `https://api.deepseek.com/v1` |
| `TERMINAL_LLM_API_KEY` | ключ LLM |
| `TERMINAL_LLM_MODEL` | модель, по умолчанию `deepseek-chat` |
| `TERMINAL_LLM_TIMEOUT` | таймаут одного запроса, default `90s` |
| `TERMINAL_LIVE_TRADING` | `1` — разрешить реальные заявки (вместе с флагом) |

Правила: значения секретов никогда не печатать/не логировать/не коммитить. Все тесты — без сети,
live-проверки только у Lead с согласия пользователя.

## 3. Структура и владельцы (write scopes)

```
cmd/terminal/                 # main, флаги, wiring           — task backend-core
terminal/static.go            # //go:embed web                — task backend-core
terminal/web/                 # SPA без сборки (HTML/CSS/JS)  — task frontend
terminal/api/                 # HTTP/JSON/SSE, DTO, статика   — task backend-core
terminal/feed/                # live native + offline seed    — task backend-core
terminal/paper/               # бумажный брокер               — task backend-core
terminal/backtest/            # движок бэктеста + стратегии   — task backtest
terminal/agent/               # LLM/tool-loop/аналитик/автономия — task agent
terminal/seeddata/embed.go    # //go:embed data/*.json.gz     — Lead
terminal/seeddata/data/       # seed-снапшот (реальные данные) — Lead
tools/terminal-seed/          # утилита снятия seed с демо     — Lead
tools/llm-stub/               # фейковый OpenAI-сервер для демо — task agent
docs/ai-terminal/             # этот документ, UI/README       — Lead/frontend
```

`go.mod` не меняется: только stdlib. `native/` меняется только Lead'ом (уже сделан фикс времени
свечей: единицы = период свечи).

## 4. Пакет `terminal` (статика)

```go
package terminal

//go:embed web
var webFS embed.FS

// WebFS returns the embedded SPA root (index.html, app.js, styles.css, ...).
func WebFS() fs.FS
```

## 5. Пакет `terminal/seeddata` (встроенный снапшот)

```go
package seeddata

//go:embed data/*.json.gz
var FS embed.FS

const Dir = "data"
```

Файл `data/seed.json.gz` — gzip(JSON) со схемой:

```jsonc
{
  "schema": 1,
  "generatedAt": "2026-11-05T16:40:00Z",   // UTC
  "source": "transaq-demo",
  "exchangeTZ": "Europe/Moscow",
  "universe": ["TQBR:SBER", "..."],
  "instruments": [
    {
      "symbol": "TQBR:SBER", "board": "TQBR", "seccode": "SBER",
      "name": "Сбербанк", "shortName": "Сбербанк", "currency": "RUB",
      "decimals": 2, "lotSize": 10, "minStep": 0.01,
      "prevClose": 292.17, "last": 293.22, "change": 1.05, "changePct": 0.36,
      "open": 292.10, "high": 294.00, "low": 291.50, "volume": 1234567,
      "bid": 293.22, "ask": 293.26,
      "orderbook": {
        "bids": [{"price": 293.22, "qty": 100}],
        "asks": [{"price": 293.26, "qty": 200}]
      },
      "spark": [292.1, 292.5],              // последние ~60 close, для спарклайна
      "trades": [                           // опционально: последние сделки (лента «Торги»)
        {"t": 1793894940, "price": 293.22, "qty": 10, "side": "buy"}
      ],
      "candles": {                          // ключ = период в секундах
        "60":   [{"t": 1793894940, "o": 293.2, "h": 293.3, "l": 293.1, "c": 293.22, "v": 1234}],
        "300":  [/* ... */],
        "900":  [/* ... */],
        "3600": [/* ... */],
        "86400":[/* ... */]
      }
    }
  ]
}
```

`t` — Unix-секунды UTC (время бара = реальный UTC-момент; native-клиент уже применил bias
сервера). Числа цен — float64 в JSON только на границе аналитики; на проводе остаются
`model.Decimal`. Seed-данные — только рыночные данные, без счетов/клиентов/логинов.

## 6. Пакет `terminal/feed` (рыночные данные)

```go
package feed

type Mode string

const (
	ModeLive    Mode = "live"
	ModeOffline Mode = "offline"
)

type Config struct {
	Mode       Mode
	Address    string        // host:port (live)
	Login      string
	Password   string
	SeedDir    string        // пусто = встроенный seeddata.FS
	Universe   []string      // "TQBR:SBER"; пусто = universe из seed
	RqDelay    time.Duration // RQ_MAIN poll, default 100ms
	HistoryTTL time.Duration // кеш истории, default 60s
}

type Status struct {
	Mode            Mode
	Connected       bool
	Message         string    // человекочитаемое состояние/ошибка
	ServerTime      time.Time // последнее известное биржевое/серверное время
	ExchangeTZ      string    // напр. "Europe/Moscow"
	SeedGeneratedAt time.Time
	LastEventAt     time.Time
}

type Instrument struct {
	Symbol, Board, SecCode, Name, ShortName, Currency string
	Decimals int
	LotSize  int64
	MinStep  float64
	Last, Change, ChangePct float64
	PrevClose, Open, High, Low float64
	Volume   int64
	Bid, Ask float64
	Spark    []float64
	UpdatedAt time.Time
}

type Bar struct {
	Time                    time.Time
	Open, High, Low, Close  float64
	Volume                  int64
}

type History struct {
	Symbol    string
	PeriodSec int
	Bars      []Bar
	Source    string // "live" | "seed"
	Note      string // напр. "демо-снапшот 05.11.2026"
}

type Level struct{ Price, Qty float64 }

type OrderBook struct {
	Symbol string
	Last     float64
	Time     time.Time
	Bids, Asks []Level // до 20 уровней, лучшие первыми
	Source   string
}

type Quote struct {
	Symbol string
	Last, Change, ChangePct, Bid, Ask float64
	Volume int64
	Time   time.Time
}

type Trade struct {
	Time time.Time
	Price float64
	Qty   int64
	Side  string // "buy" | "sell" | ""
}

type Event struct {
	Type string // "quote" | "orderbook" | "portfolio" | "orders" | "status"
	Data any
}

type Service struct{ /* ... */ }

func New(cfg Config) (*Service, error)      // offline: грузит seed сразу; live: валидирует конфиг
func (s *Service) Start(ctx context.Context) error // live: Dial + run-loop с реконнектом; offline: no-op
func (s *Service) Close() error
func (s *Service) Status() Status
func (s *Service) Universe() []string
func (s *Service) Instruments() []Instrument
func (s *Service) Instrument(symbol string) (Instrument, bool)
func (s *Service) Quote(symbol string) (Quote, bool)
func (s *Service) Candles(ctx context.Context, symbol string, periodSec, count int) (History, error)
func (s *Service) OrderBook(ctx context.Context, symbol string) (OrderBook, error)
func (s *Service) Trades(ctx context.Context, symbol string, limit int) ([]Trade, error)
func (s *Service) Subscribe(fn func(Event)) (cancel func())
```

Требования:

- `periodSec` принимает 60, 300, 900, 3600, 86400 (UI: 1м/5м/15м/1ч/1д). В live период
  резолвится в id `<candlekinds>` по секундам. Ошибка — `*feed.Error` с кодом `bad_period`.
- live: `native.Client` с `ClientConfig{Address, Login, Password, RqDelay}`; после `Dial` — подписка
  `Subscription{Quotations, Quotes, AllTrades}` на universe (батчами по 50); `Run` в горутине;
  обновление кешей из `*model.Quotation`, `*model.Quotes`, `*model.Trade`, `model.ServerStatus`;
  обрыв — реконнект с backoff (2s→30s), `Status.Message` = состояние, старые данные не теряются.
- live: `Candles` → `client.History` с таймаутом 25s; кеш `map[symbol|period]*History` с TTL.
- offline: `Candles`/`OrderBook`/`Trades` из seed; `Quote` — последний бар 1м; `Start` no-op;
  `Status.Connected=true`, `Message="офлайн-снапшот"`.
- никаких float64 на проводе: `model.Decimal` → `float64` только при отдаче в JSON/аналитику.
- никаких секретов в логах; логировать только факт логина и ошибки без значений.

## 7. Пакет `terminal/paper` (бумажный брокер)

```go
package paper

type Config struct {
	InitialCash float64
	FeeBps      float64 // default 5
}

type Side string

const (Buy Side = "buy"; Sell Side = "sell")

type OrderType string

const (Market OrderType = "market"; Limit OrderType = "limit")

type Status string

const (
	StatusNew Status = "new"; StatusFilled = "filled"; StatusPartial = "partial"
	StatusCancelled = "cancelled"; StatusRejected = "rejected"
)

type OrderRequest struct {
	Symbol string; Side Side; Qty int64; Type OrderType
	Price float64; Source string // "ui" | "agent" | "autonomy"
}

type Order struct {
	ID, Symbol string; Side Side; Type OrderType
	Qty, Filled int64
	Price, AvgFillPrice float64
	Status Status
	CreatedAt, UpdatedAt time.Time
	Source, Message string
}

type Trade struct {
	ID, OrderID, Symbol string; Side Side
	Qty int64; Price, Fee float64; Time time.Time; Source string
}

type Position struct {
	Symbol string; Qty int64; AvgPrice, RealizedPnL float64
}

type QuoteView struct {
	Symbol string; Last, Bid, Ask float64; Time time.Time
}

type Snapshot struct {
	Cash, RealizedPnL, Fees float64
	Positions []Position
	Orders []Order
	Trades []Trade
	UpdatedAt time.Time
}

func New(cfg Config) *Broker
func (b *Broker) Place(req OrderRequest, q QuoteView) (Order, error)
func (b *Broker) Cancel(id string) (Order, error)
func (b *Broker) OnQuote(q QuoteView) []Event // исполнение выставленных лимиток
func (b *Broker) Snapshot() Snapshot
```

Правила исполнения: market buy = ask (нет ask → last), market sell = bid (нет bid → last),
проскальзывание 0 (упрощение); limit исполняется, если marketable на момент постановки, иначе
висит и исполняется в `OnQuote` при касании (buy: ask ≤ price; sell: bid ≥ price);
частичных исполнений в итерации 1 нет (`Filled = Qty` при fill). Комиссия `Fee = |price*qty| *
FeeBps/10000` списывается/учитывается в `Fees`, `RealizedPnL` — по средней цене. Продажа больше
позиции — reject (`short not supported`). Кэш не может стать отрицательным — reject.
Брокер потокобезопасен (mutex). `Snapshot` — копии срезов.

## 8. Пакет `terminal/backtest` (движок)

```go
package backtest

type Bar struct {
	Time                   time.Time
	Open, High, Low, Close float64
	Volume                 int64
}

type Param struct {
	Key, Title string
	Default, Min, Max, Step float64
}

type StrategyInfo struct {
	ID, Name, Description string
	Params []Param
}

type Action int

const (Hold Action = iota; Buy; Sell)

type Strategy interface {
	ID() string
	Params() map[string]float64
	// OnBar returns the action decided on the close of bars[i]. It may look at
	// bars[0..i] only (no lookahead).
	OnBar(bars []Bar, i int) Action
}

func Strategies() []StrategyInfo
func NewStrategy(id string, params map[string]float64) (Strategy, error)

type Request struct {
	Symbol      string
	PeriodSec   int
	Bars        []Bar
	Strategy    string
	Params      map[string]float64
	InitialCash float64 // default 1_000_000
	FeeBps      float64 // default 5
	SlippageBps float64 // default 1
	RiskPct     float64 // доля эквити в позиции, default 100
}

type Trade struct {
	EntryTime, ExitTime time.Time
	Side                string // "long"
	EntryPrice, ExitPrice float64
	Qty                 int64
	PnL, PnLPct         float64
	BarsHeld            int
	Reason              string
}

type EquityPoint struct {
	Time         time.Time
	Equity       float64
	Benchmark    float64
	DrawdownPct  float64
}

type Metrics struct {
	InitialCash, FinalEquity, TotalReturnPct, BenchmarkReturnPct float64
	MaxDrawdownPct, Sharpe, WinRatePct, ProfitFactor, AvgTradePct, ExposurePct float64
	Trades, Bars int
	From, To time.Time
}

type Result struct {
	Symbol, Strategy string
	PeriodSec        int
	Params           map[string]float64
	Metrics          Metrics
	Equity           []EquityPoint
	Trades           []Trade
	Warnings         []string
}

func Run(req Request) (Result, error)
```

JSON-ключи `Result` (заморожены, их использует UI и агент; теги в `types.go`):

```jsonc
{"symbol":"TQBR:SBER","strategy":"sma_cross","periodSec":300,"params":{"fast":10,"slow":30},
 "metrics":{"initialCash":1000000,"finalEquity":1012345.6,"totalReturnPct":1.2345,
   "benchmarkReturnPct":-0.45,"maxDrawdownPct":-1.8,"sharpe":1.2,"winRatePct":55.6,
   "profitFactor":1.9,"avgTradePct":0.45,"exposurePct":62.3,"trades":9,"bars":700,
   "from":"...","to":"..."},
 "equity":[{"t":"...","value":1000000,"benchmark":1000000,"drawdownPct":0}],
 "trades":[{"entryTime":"...","exitTime":"...","side":"long","entryPrice":293.2,
   "exitPrice":294.1,"qty":100,"pnl":90,"pnlPct":0.3,"barsHeld":12,"reason":"signal"}],
 "warnings":[]}
```

`equity[].t` — RFC3339 (на проводе Go `time.Time`), `value` — эквити, `benchmark` — buy&hold.


Стратегии итерации 1 (id, параметры):

1. `sma_cross` — `fast` (10), `slow` (30): long при пересечении fast>slow вверх, выход при
   fast<slow; вход/выход по сигналу на закрытии, исполнение по open следующего бара.
2. `rsi_reversion` — `period` (14), `oversold` (30), `overbought` (55).
3. `donchian_breakout` — `period` (20): вход close > max(high за предыдущие `period`), выход
   close < min(low).
4. `momentum` — `lookback` (20), `thresholdPct` (1.0): вход при доходности за lookback >
   threshold, выход при < 0.

Метрики (все в процентах, где `Pct`):

- `TotalReturnPct = (FinalEquity/InitialCash-1)*100`.
- `BenchmarkReturnPct` — buy&hold первого close до последнего close без комиссий.
- `MaxDrawdownPct` — минимум по equity drawdown (отрицательное число).
- `Sharpe` — mean/std доходностей баров × sqrt(annualization), annualization =
  `365*24*3600/PeriodSec`; при std=0 → 0 (только для сравнения стратегий, в UI подписать).
- `WinRatePct` — доля прибыльных сделок; `ProfitFactor` = сумма выигрышей / |сумма проигрышей|;
  `AvgTradePct` — средний PnL% сделки; `ExposurePct` — доля баров с открытой позицией.
- `Warnings` — мало баров (< 50), нулевые сделки, high drawdown и т.п.

Нет lookahead: `OnBar(i)` не читает бары > i; fill по `Open[i+1]`; slippage применяется против
направления; комиссия с оборота. Long-only. Позиция — целое число единиц (акция, не лоты) по
цене fill; если средств не хватает на 1 шт — позиция не открывается (не ошибка).

## 9. Пакет `terminal/agent` (AI-агент, tool loop, автономия)

```go
package agent

type LLMConfig struct {
	BaseURL, APIKey, Model string
	Timeout                time.Duration
	Enabled                bool // вычисляется: BaseURL != "" && APIKey != ""
}

type RiskLimits struct {
	MaxPositionPct float64 // max доля портфеля в одной позиции, default 25
	MaxOrderPct    float64 // max доля кэша на одну заявку, default 20
	StopLossPct    float64 // default 2
	TakeProfitPct  float64 // default 5
	DailyLossPct   float64 // default 3
	MaxTradesPerHour float64 // default 30
}

type Config struct {
	LLM              LLMConfig
	Universe         []string
	DefaultSymbol    string
	DefaultPeriodSec int
	Risk             RiskLimits
	FeeBps           float64
	BacktestHistory  int // сколько последних бэктестов хранить, default 20
	Now              func() time.Time
}

type InstrumentRef struct {
	Symbol, Name string
	Last, ChangePct float64
}

type Quote struct {
	Symbol string
	Last, Bid, Ask, ChangePct float64
	Time time.Time
}

type Level struct{ Price, Qty float64 }

type OrderBook struct {
	Symbol string
	Bids, Asks []Level
	Last float64
}

// DataSource реализует адаптер feed в cmd/terminal.
type DataSource interface {
	Instruments() []InstrumentRef
	Quote(symbol string) (Quote, bool)
	OrderBook(symbol string) (OrderBook, error)
	History(ctx context.Context, symbol string, periodSec, count int) ([]backtest.Bar, error)
}

type PlaceOrder struct {
	Symbol string; Side string; Qty int64; Type string; Price float64
}

type OrderView struct {
	ID, Symbol, Side, Type, Status string
	Qty, Filled int64
	Price, AvgFillPrice float64
	CreatedAt time.Time
}

type PositionView struct {
	Symbol string; Qty int64
	AvgPrice, Price, Value, Profit, ProfitPct float64
}

type PortfolioView struct {
	TotalValue, Cash, DayChange, DayChangePct float64
	Positions []PositionView
}

// Broker реализует адаптер paper.Broker в cmd/terminal.
type Broker interface {
	Place(PlaceOrder) (OrderView, error)
	Cancel(id string) (OrderView, error)
	Orders() []OrderView
	Portfolio() PortfolioView
	Cash() float64
}

type ToolInfo struct {
	Name, Title, Description string
	Input json.RawMessage // JSON Schema входных аргументов
}

type ToolCall struct {
	Name string
	Args map[string]any
	Result any
	Error string
	DurationMs int64
}

type Artifact struct {
	Kind string // "backtest" | "chart_overlay" | "autonomy" | "hints"
	Payload any
}

type ChatRequest struct {
	Message string
	ConversationID string // итерация 1: не сохраняем историю, поле для UI
}

type ChatResponse struct {
	Answer string
	ToolCalls []ToolCall
	Artifacts []Artifact
	LLM bool
	ElapsedMs int64
}

type Event struct {
	Type string // "tool_call" | "tool_result" | "message" | "autonomy" | "log" | "backtest"
	Data any
}

type AutonomyConfig struct {
	Symbol string
	PeriodSec int
	Strategy string
	Params map[string]float64
	Speed float64 // баров в секунду, 1..60, default 5
	Budget float64 // max деньги на стратегию, default 200000
	MaxPositionPct, StopLossPct, TakeProfitPct, DailyLossPct float64
	Live bool // итерация 1: только false; true -> ошибка "live autonomy is not implemented"
}

type LogEntry struct {
	Time time.Time
	Kind, Message string
	Data any
}

type AutonomyStatus struct {
	Running bool
	Config AutonomyConfig
	StartedAt, LastActionAt time.Time
	Trades int
	PnL, PnLPct float64
	Log []LogEntry
}

type LLMStatus struct {
	Enabled bool
	Model string
	Note string
}

type Status struct {
	Autonomy AutonomyStatus
	LLM LLMStatus
}

type MarketHints struct {
	Symbol string
	PeriodSec int
	AsOf time.Time
	SMA20, SMA50, RSI14, ATR14, VolatilityPct float64
	Trend string // "up" | "down" | "flat"
	Support, Resistance float64
	Breadth map[string]int // {"advancers": n, "decliners": n, "unchanged": n}
	Note string
}

func New(cfg Config, data DataSource, broker Broker) (*Engine, error)
func (e *Engine) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
func (e *Engine) Tools() []ToolInfo
func (e *Engine) Status() Status
func (e *Engine) StartAutonomy(ctx context.Context, cfg AutonomyConfig) error
func (e *Engine) StopAutonomy() error
func (e *Engine) Subscribe(fn func(Event)) (cancel func())
func (e *Engine) Backtests() []backtest.Result
func (e *Engine) Hints(ctx context.Context, symbol string, periodSec int) (MarketHints, error)
```

### 9.1 Инструменты агента (tool calling без MCP)

Все инструменты выполняются **в процессе** бэкенда (это и есть «напрямую, без MCP»); каждый вызов
попадает в `ChatResponse.ToolCalls` и в SSE-событие `agent`. Имена и JSON-аргументы:

| Имя | Аргументы | Результат |
|---|---|---|
| `list_instruments` | `{}` | `[{symbol,name,last,changePct}]` |
| `get_market_hints` | `{symbol, periodSec?}` | `MarketHints` |
| `get_history` | `{symbol, periodSec, count}` | `{symbol,periodSec,source,bars:[{t,o,h,l,c,v}]}` (последние ≤600) |
| `run_backtest` | `{symbol, periodSec?, strategy, params?, initialCash?, feeBps?, slippageBps?, count?}` | `backtest.Result` (equity/trades полностью) |
| `compare_strategies` | `{symbol, periodSec?, count?}` | `{ranked:[{strategy,metrics}], best:{strategy,params,result}}` |
| `get_quote` | `{symbol}` | `{symbol,last,bid,ask,changePct,time,book:{bids,asks}}` |
| `get_portfolio` | `{}` | `PortfolioView` |
| `get_strategies` | `{}` | `[StrategyInfo]` |
| `place_order` | `{symbol,side,qty,type?,price?}` | `OrderView` (бумажный; риск-лимиты проверяются) |
| `cancel_order` | `{orderId}` | `{order:OrderView}` |
| `start_autonomy` | `AutonomyConfig` без `Live` | `AutonomyStatus` (только если risk-check прошёл) |
| `stop_autonomy` | `{}` | `AutonomyStatus` |
| `get_agent_status` | `{}` | `Status` |
| `get_backtest_history` | `{}` | `[backtest.Result]` (без bars/equity? equity нужна UI — оставить equity) |

Артефакты для UI: `run_backtest`/`compare_strategies` добавляют
`Artifact{Kind:"backtest", Payload: backtest.Result}`; `start_autonomy` —
`Artifact{Kind:"autonomy", ...}`; `get_market_hints` — `Artifact{Kind:"hints", ...}`.

### 9.2 LLM-режим

- OpenAI-compatible `POST {BaseURL}/chat/completions` (base URL уже содержит `/v1`), поля
  `model`, `messages`, `tools:[{type:"function",function:{name,description,parameters}}]`,
  `tool_choice:"auto"`, `temperature:0.2`, `stream:false`. Ответ — `choices[0].message.tool_calls[]`
  (`function.name`, `function.arguments` — JSON-строка). До 8 итераций tool-loop, затем финальный
  ответ без tools (если модель не вернула текст — суммаризация локальным аналитиком по артефактам).
- Системный промпт: русский, роль — торговый аналитик терминала, только инструменты из списка,
  не выдумывать цифры, предупреждать о рисках, отвечать ≤ 200 слов, использовать реальные данные
  инструментов. Если запрос про «запусти автоторговлю» — сначала бэктест, потом предложить/запустить
  автономию на бумажном счёте.
- Ошибка LLM/сети/таймаута → fallback на локального аналитика, поле `LLM:false`, в ответ не
  возвращать текст ошибки с секретами (`"LLM недоступен: <class>"`).
- Без env — `LLM.enabled=false`, UI показывает «локальный аналитик».

### 9.3 Локальный аналитик (offline fallback и детерминированный демо-режим)

Пайплайн без LLM: разобрать намерение из русского/английского текста (тикер латиницей/название,
период «1м/5м/15м/1ч/день», капитал, стратегии по ключевым словам «SMA/скользящая/RSI/пробой/
моментум»), выполнить реальные tool-вызовы (`compare_strategies` → `run_backtest` лучшей →
`get_market_hints`), сформировать ответ на русском с метриками и рисками; если в сообщении есть
«запусти/авто/торгуй» — предложить и запустить `start_autonomy` (paper), только если найден
подходящий бэктест. Ответ всегда содержит `ToolCalls`/`Artifacts`, чтобы UI показал те же карточки,
что и в LLM-режиме.

### 9.4 Автономия (paper replay)

- Режим: replay исторических баров с `Speed` баров/сек (грид 1..60; default 5), через
  `backtest.NewStrategy`, действия на закрытии бара, бумажное исполнение по open следующего бара с
  комиссией `FeeBps`.
- Риск-лимиты (проверяются перед каждой заявкой): `Budget`, `MaxPositionPct` портфеля,
  `MaxOrderPct` кэша, `StopLossPct`, `TakeProfitPct`, `DailyLossPct` (от начального equity),
  `MaxTradesPerHour`. Нарушение → сделка не отправляется, лог `risk_block`.
- Ведёт `PnL` (mark-to-market по последнему обработанному бару), `Trades`, `Log` (ring 500),
  шлёт SSE-события `agent`. Остановка — ручная, kill-switch (`POST /api/agent/autonomy action=stop`)
  и автоматическая при достижении `DailyLossPct` (статус `stopped_by_risk`, лог).
- `Live:true` → ошибка `live autonomy is not implemented in iteration 1`.
- После Stop статус сохраняет итоговые PnL/лог для UI.

## 10. Пакет `terminal/api` (HTTP/JSON/SSE)

```go
package api

type Config struct {
	Version string
	LiveTradingAllowed bool // -live-trading && TERMINAL_LIVE_TRADING=1
	PaperInitialCash float64
	SeedGeneratedAt  time.Time
	Web              fs.FS // статика SPA; в main — terminal.WebFS()
}

// Интерфейсы объявлены в api, чтобы обработчики тестировались фейками, а
// конкретные feed.Service/paper.Broker/agent.Engine подключались в main через
// адаптеры. Сигнатуры должны совпадать с контрактами пакетов (§6–§9).
type FeedAPI interface {
	Status() feed.Status
	Instruments() []feed.Instrument
	Instrument(symbol string) (feed.Instrument, bool)
	Quote(symbol string) (feed.Quote, bool)
	Candles(ctx context.Context, symbol string, periodSec, count int) (feed.History, error)
	OrderBook(ctx context.Context, symbol string) (feed.OrderBook, error)
	Trades(ctx context.Context, symbol string, limit int) ([]feed.Trade, error)
	Subscribe(fn func(feed.Event)) (cancel func())
}

type PaperAPI interface {
	Place(req paper.OrderRequest, q paper.QuoteView) (paper.Order, error)
	Cancel(id string) (paper.Order, error)
	OnQuote(q paper.QuoteView) []paper.Event
	Snapshot() paper.Snapshot
}

type AgentAPI interface {
	Chat(ctx context.Context, req agent.ChatRequest) (agent.ChatResponse, error)
	Status() agent.Status
	StartAutonomy(ctx context.Context, cfg agent.AutonomyConfig) error
	StopAutonomy() error
	Subscribe(fn func(agent.Event)) (cancel func())
	Backtests() []backtest.Result
	Hints(ctx context.Context, symbol string, periodSec int) (agent.MarketHints, error)
	Tools() []agent.ToolInfo
}

type Deps struct {
	Feed     FeedAPI
	Paper    PaperAPI
	Agent    AgentAPI
	Backtest func(backtest.Request) (backtest.Result, error) // для HTTP-бэктеста
}

func New(cfg Config, deps Deps) (*Server, error)
func (s *Server) Handler() http.Handler
```

Все ответы — `application/json; charset=utf-8`, ошибки:

```json
{"error":{"code":"bad_request","message":"что не так"}}
```

Коды: `bad_request` 400, `not_found` 404, `conflict` 409, `unavailable` 503,
`live_trading_disabled` 403, `internal` 500. Маппинг ошибок агента: повторный `start` при
работающем runner → 409 `conflict`; невалидный конфиг/`live=true` → 400; нереализованное → 503.

### 10.1 REST

| Метод/путь | Тело/параметры | Ответ |
|---|---|---|
| `GET /api/health` | — | `{ok,version,mode,connected,message,serverTime,exchangeTZ,seedGeneratedAt,llm:{enabled,model,note},trading:{paper:true,live:bool}}` |
| `GET /api/instruments` | — | `{serverTime, instruments:[Instrument]}` (см. §10.2) |
| `GET /api/instruments/{symbol}/candles?period=300&count=500` | period sec, count ≤ 5000 | `{symbol,periodSec,source,note,candles:[{t,o,h,l,c,v}]}` |
| `GET /api/instruments/{symbol}/orderbook` | — | `{symbol,last,time,bids,asks,source}` |
| `GET /api/instruments/{symbol}/trades?limit=50` | limit ≤ 200 | `{symbol,trades:[{time,price,qty,side}]}` |
| `GET /api/instruments/{symbol}/hints?period=300` | — | `{symbol,periodSec,asOf,sma20,sma50,rsi14,atr14,volatilityPct,trend,support,resistance,breadth,note}` |
| `GET /api/portfolio` | — | `{totalValue,cash,dayChange,dayChangePct,realizedPnl,fees,marginPct,positions:[...],updatedAt}` |
| `GET /api/orders` | — | `{orders:[Order]}` |
| `GET /api/trades` | — | `{trades:[Trade]}` |
| `POST /api/orders` | `{symbol,side,qty,type,price?,confirm?}` | 201 `{order}`; live: `confirm:"LIVE"` обязателен |
| `DELETE /api/orders/{id}` | — | `{ok:true}` |
| `GET /api/backtest/strategies` | — | `{strategies:[StrategyInfo]}` |
| `POST /api/backtest` | `{symbol,periodSec,strategy,params?,initialCash?,feeBps?,slippageBps?,count?}` | `{result: backtest.Result}` |
| `GET /api/agent/tools` | — | `{tools:[{name,title,description,input}]}` |
| `POST /api/agent/chat` | `{message,conversationId?}` | `ChatResponse` |
| `GET /api/agent/status` | — | `agent.Status` + `{autonomy,llm}` |
| `POST /api/agent/autonomy` | `{action:"start"\|"stop", config?}` | `{autonomy: AutonomyStatus}` |
| `GET /api/agent/autonomy/log?limit=200` | — | `{log:[LogEntry]}` |
| `GET /api/agent/backtests` | — | `{results:[backtest.Result]}` |
| `GET /api/events` | SSE | §10.3 |
| `GET /` и `/assets/*` | — | SPA из `terminal.WebFS()` (index.html fallback) |

`symbol` = `BOARD:SECCODE` в URL нужно кодировать (`TQBR%3ASBER`) — Go/JS `encodeURIComponent`;
сервер обязан принимать и `TQBR:SBER` в path (роутер `r.URL.Path` split по последнему `:`).

### 10.2 JSON-формы

```jsonc
// Instrument
{"symbol":"TQBR:SBER","board":"TQBR","seccode":"SBER","name":"Сбербанк","shortName":"Сбербанк",
 "currency":"RUB","decimals":2,"lotSize":10,"minStep":0.01,"prevClose":292.17,"last":293.22,
 "change":1.05,"changePct":0.36,"open":292.1,"high":294.0,"low":291.5,"volume":1234567,
 "bid":293.22,"ask":293.26,"spark":[292.1,292.5],"updatedAt":"2026-11-05T16:44:00Z"}

// Order (paper)
{"id":"ord_000001","symbol":"TQBR:SBER","side":"buy","type":"market","qty":10,"filled":10,
 "price":0,"avgFillPrice":293.26,"status":"filled","createdAt":"...","updatedAt":"...",
 "source":"ui","message":""}

// Position
{"symbol":"TQBR:SBER","name":"Сбербанк","qty":10,"avgPrice":293.26,"price":293.22,
 "value":2932.2,"dayChangePct":0.36,"profit":-0.4,"profitPct":-0.01,"realizedPnl":0}

// Portfolio
{"totalValue":1002932.2,"cash":997067.8,"dayChange":-0.4,"dayChangePct":0.0,
 "realizedPnl":0,"fees":0.15,"marginPct":100,"positions":[Position],"updatedAt":"..."}

// LogEntry
{"time":"...","kind":"signal|order|fill|risk_block|start|stop|error","message":"...","data":{}}

// ChatResponse
{"answer":"...","toolCalls":[{"name":"run_backtest","args":{},"result":{},"error":"",
 "durationMs":12}],"artifacts":[{"kind":"backtest","payload":{}}],"llm":false,"elapsedMs":42}
```

`time` во всех JSON — RFC3339 UTC. Цены — числа; UI форматирует с `decimals` инструмента.

### 10.3 SSE `/api/events`

`Content-Type: text/event-stream`, без кеша. Каждое сообщение:

```
event: message
data: {"type":"quote","data":{...}}

```

Типы и `data`:

| type | data | Частота |
|---|---|---|
| `quote` | `{symbol,last,change,changePct,bid,ask,volume,time}` | live: 1s / offline: 1s (последний бар) |
| `portfolio` | `Portfolio` (полный, как GET) | при изменении/1s |
| `orders` | `{orders:[Order]}` | при изменении |
| `agent` | `{type:"tool_call"\|"tool_result"\|"message"\|"autonomy"\|"log"\|"backtest", ...}` | по мере событий агента |
| `status` | `{ok,mode,connected,message,serverTime}` | 5s |
| `heartbeat` | `{}` | 15s |

Событие агента — обёртка `{"type":"agent","data":{"type":"<agentEventType>","data":...}}`; для
автономии в `data` приходит **частичный тик** прогресса, а не полный `AutonomyStatus`:
`{"running":true,"symbol":"TQBR:SBER","periodSec":300,"time":"...","price":293.2,
"position":100,"trades":1,"pnl":12.3,"pnlPct":0.01}`; полный статус (config/log/startedAt) — из
`GET /api/agent/status`.

Сервер: hub с буферизованными каналами (256), медленные клиенты отбрасываются, поддержка
`r.Context().Done()`. CORS не нужен (same origin). `Cache-Control: no-store`.

### 10.4 Безопасность

- Сервер слушает loopback по умолчанию; при `-addr 0.0.0.0:*` — предупреждение в лог (не запрещено).
- Реальные заявки: разрешены только если `cfg.LiveTradingAllowed` **и** тело содержит
  `"confirm":"LIVE"`; иначе 403 `live_trading_disabled`. В итерации 1 live-ордера в API
  реализованы как заглушка с этим кодом (маршрутизация на биржу — roadmap), но UI обязан
  показывать статус «только бумага».
- Никакой аутентификации в итерации 1 (loopback); в README явно указать запуск только локально.

## 11. Frontend (`terminal/web/`, без сборки)

Файлы (минимум): `index.html`, `styles.css`, `app.js`, `chart.js`, `ai.js`, `api.js`,
`icons.js` (inline SVG), `mock/` (фикстуры для `?mock=1`). Только ES-модули, никаких внешних
CDN/шрифтов — офлайн.

Макет (десктоп ≥1280×800, референс — скриншот Finam):

1. **Header (52px)**: логотип/название «AI Terminal», поиск инструмента, бейдж режима
   `ДЕМО`/`LIVE`, точка соединения + серверное время, жёлтая кнопка «AI-агент», иконки
   уведомлений/помощи.
2. **Left rail (56px)**: иконки разделов (избранное, портфель, терминал, идеи, календарь,
   уведомления, почта, глобус, чат, обучение, помощь). Активный раздел подсвечен; рабочие —
   «Избранное», «Портфель», «AI-агент»; остальные декоративные (tooltip «скоро»).
3. **Watchlist (≈330px)**: селектор счёта/портфеля (демо-счёт), поиск-фильтр, заголовок
   «Инструмент / Цена», строки: круглый бейдж-тикер, название + борд/время, спарклайн (canvas),
   цена (крупно), изменение ₽ и % (зелёный/красный). Выбор строки → график/стакан/тикет.
4. **Center**: шапка инструмента (название, крупная цена, изменение, `MOEX · HH:MM:SS`, звезда,
   кнопка «Заявка» жёлтая, «Опционы» disabled), тулбар периодов `1м 5м 15м 1ч 1д` + тип графика,
   строка OHLC, canvas-график (свечи/линия, объёмы, сетка, правая ценовая ось, нижняя ось времени,
   метка последней цены, перекрестие с подсказкой). Справа от графика — колонка табов
   `Стакан | Торговля | Торги | Инфо | Дивиденды`: стакан (Bid/Vol/Ask, 10–20 уровней,
   красные/зелёные полосы объёма, клик уровня → тикет), тикет (buy/sell, qty, market/limit,
   цена, стоимость, комиссия, «Купить»/«Продать», бейдж «Бумажный счёт»), тикер-лента, инфо
   (лот, шаг, борд, валюта, дневной диапазон, AI-подсказки), дивиденды («нет данных»).
5. **Bottom (≈300px, сворачиваемая)**: summary-строка (стоимость портфеля, дневная прибыль,
   свободные деньги, гейдж маржи), табы `Портфель | Заявки | История | Аналитика`, таблица:
   колонки как в Finam (Инструмент, Биржа, Кол-во, Цена, Изменение, Оценка, Обеспечение,
   Средняя цена, Прибыль за день, Прибыль, Прибыль %, меню). Заявки — отмена; История — сделки;
   Аналитика — последние бэктесты агента (карточки с метриками, клик → overlay на график).
6. **AI-панель (drawer справа, ≈440px, overlay)**: заголовок «AI-агент» + статус LLM
   («DeepSeek/локальный аналитик»), быстрые промпты, чат (сообщения, tool-call карточки
   свёрнутые, артефакты: метрики бэктеста + equity-график + кнопка «Показать на графике»,
   карточка автономии), панель автономии (инструмент, стратегия, параметры, бюджет, риск-лимиты,
   скорость, Старт/Стоп, статус PnL + лог), поле ввода. Живые события — SSE `agent`.
7. **Status bar (28px)**: версия, режим/источник данных, время сервера, подсказки горячих клавиш.
8. **Режим mock**: `?mock=1` — `api.js` отдаёт фикстуры из `mock/` и эмулирует SSE-тики,
   чтобы вёрстку можно было проверять через `python3 -m http.server` без бэкенда. В боевом
   режиме mock-код не активен.

Стиль: светлая тема, фон `#f2f3f5`, панели `#fff`, границы `#e6e8eb`, текст `#1b1f24`,
вторичный `#7a828c`, акцент `#ffcf33` (жёлтая кнопка), рост `#0f9d6b`, падение `#e5484d`,
left rail `#23262b`, радиус панелей 8–10px, плотные таблицы 32–36px, tabular-nums, без
внешних шрифтов. Все интерактивные элементы — доступные (`aria-label`, focus-visible).

## 12. `cmd/terminal` (wiring)

`main` читает флаги/env → `feed.New` → `feed.Start` (live) → `paper.New` → `agent.New` (адаптеры:
`feedAdapter`, `paperAdapter` в `cmd/terminal/wiring.go`) → `api.New` → `http.Server` с
`ReadHeaderTimeout`, graceful shutdown по SIGINT/SIGTERM, лог одной строкой о режиме и URL без
секретов. `BACKTEST`-функция для API: получает бары из feed, конвертирует в `backtest.Bar`,
вызывает `backtest.Run`, кеширует результаты для `/api/agent/backtests` через агент.

## 13. Проверки и демо-сценарий

1. `tools/terminal-seed` (Lead) снимает seed с демо-сервера: юниверс 10–14 бумаг TQBR, свечи
   `60×1200, 300×700, 900×400, 3600×300, 86400×260`, стаканы/котировки/лента. Секреты не пишутся;
   снятый снапшот — `2026-11-05`, 11 бумаг TQBR.
2. `go run ./cmd/terminal -mode offline` → UI на `http://127.0.0.1:8090`.
3. Демо: выбрать SBER → график 5м; в AI-панели попросить «протестируй лучшую стратегию на SBER
   за месяц» → tool-cards и equity; «запусти автоторговлю» → автономия в paper-replay, лог,
   PnL; отобразить equity/сделки на графике; портфель/заявки/история обновляются.
4. `curl /api/instruments` — реальные цены; `curl -X POST /api/backtest` — реальная история.
5. Live read-only: `-mode live` с env биржи → те же UI-данные; реальная торговля выключена.
6. Deep links демо (проверено): `/?view=ai`, `/?view=ai&ask=<urlencoded>`, `/?tab=analytics`,
   `/?symbol=TQBR:SBER` — детерминированные состояния UI для демонстрации и скриншотов.
7. Проверки: `gofmt -l $(git ls-files '*.go')`, `go vet ./...`, `go build ./...`,
   `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...`, `CGO_ENABLED=0 GOOS=darwin
   GOARCH=arm64 go build ./...`, `go test -race ./...`, `node --check` для JS, headless-Chrome
   скриншоты 1680×1050 (Lead).

## 14. Роли и границы (RTC)

| Задача (доска) | Владелец | Write scope |
|---|---|---|
| T-backend: feed/api/paper/wiring | backend-core | `terminal/{feed,api,paper}/`, `cmd/terminal/`, `terminal/static.go` |
| T-backtest: движок | backtest-dev | `terminal/backtest/` |
| T-agent: LLM/tools/аналитик/автономия | agent-dev | `terminal/agent/`, `tools/llm-stub/` |
| T-frontend: SPA | frontend-dev | `terminal/web/` |
| T-seed+integration: seed, docs, README, ревью | Lead | `terminal/seeddata/`, `tools/terminal-seed/`, `docs/`, `README.md` |

Коммиты делает только Lead после проверок и отчёта владельца. Тесты не ходят в сеть и не требуют
секретов. `go.mod` не меняется. Teammates не подключаются к демо-серверу и не запускают live-торговлю.
