# TRANSAQ Gateway v1 + dsh-trading Connector Implementation Plan

> **Note:** references to `dsh-ai-terminal/` in this plan are historical — the plugin moved to https://github.com/kmlebedev/dsh-trading-transaq (PR 69).

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the dsh-trading terminal see the Russian market (MOEX via Finam/TRANSAQ) by exposing a stable read-only gateway contract from `cmd/terminal` and a thin, dependency-free JS connector package that registers market `ru` / provider `finam` in dsh-trading's registries.

**Architecture:** `cmd/terminal` stays the only process that talks TRANSAQ. A new versioned read-only HTTP surface `/api/gw/v1/*` (same package `terminal/api`, behind the existing `Guard` and token) serves instruments, quotes, candles, order book, tape and the paper account in a format designed for external adapters: prices as decimal strings, times as epoch ms, units named explicitly. The connector `dshtrading-connector-transaq/` is plain ESM JS (like `dsh-ai-terminal/`) with no runtime dependencies. It matches dsh-trading's `MarketDataService` / `TradeService` contracts structurally, registers them through `ctx.get('tradingMarketDataRegistry' | 'tradingTradeRegistry')` and adds read-only `ru_*` agent tools. It imports no dsh-trading code, so their PolyForm-NC license does not reach it.

**Tech Stack:** Go 1.27 stdlib (`net/http`, `strconv`); Node ≥ 24 ESM, `node:test`, global `fetch`; no npm dependencies.

**Spec:** [docs/ai-terminal/adr-dsh-trading.md](../../ai-terminal/adr-dsh-trading.md) — option D, items 2–3. Upstream contracts mirrored here, as read at dsh-trading `b3c9b96`: `packages/api/src/index.ts` (`Ticker` :53, `Kline` :75, `Orderbook` :214, `TradeTick` :223, `MarketDataService` :734, `TradeService` :860, `TradingErrorCode` :896, registries :1031–1078), `packages/connector-qmt/src/dataplane.ts` (registration pattern).

## Global Constraints

- No dsh-trading source file is copied or imported, not even as a type import. Compatibility is structural only. Their license is PolyForm Noncommercial 1.0.0.
- `go.mod` unchanged; `terminal/` stays stdlib-only; `CGO_ENABLED=0`.
- The connector has `"dependencies": {}` and `"private": true`; npm publishing is out of scope.
- Gateway v1 is **GET-only**. No live or paper order placement through it. `placeOrder` with `dryRun: false` is always rejected with `TRADING_LIVE_TRADING_DISABLED`.
- No `ru_place_order` / `ru_cancel_order` tool is registered: dsh-trading's approval gate regex `^(?:crypto|us|cn|hk)_(?:place|cancel)_order$` does not cover `ru`.
- Secrets: the gateway token is read from the env var named by `tokenRef` (default `TERMINAL_API_TOKEN`) and is never logged or put into error messages. `TC_*` / `TRANSAQ_*` are never read by the connector.
- Market key `ru`, provider slug `finam`, Cordis row id `dsh-trading-ru-connector-transaq`.
- Canonical symbol `SECCODE.BOARD` (e.g. `SBER.TQBR`); gateway/native symbol `BOARD:SECCODE` (`TQBR:SBER`).
- Go checks before every commit: AGENTS.md §7 (`make verify`, with `GOCACHE="$PWD/.cache/go-build"` in the DSH sandbox). JS: `node --test dshtrading-connector-transaq/test/`.
- Tests touch no network beyond an ephemeral loopback `http.Server` / `httptest.Server`.

## Review Focus

1. **Bare ticker `SBER`** typed by a user or the agent: resolve to the unique `SECCODE` among instruments; if several boards have it, prefer `TQBR`, otherwise `TRADING_UNSUPPORTED_SYMBOL` with the candidate list. Test in Task 4.
2. **Gateway down or restarted with a new token**: every call fails fast with `TRADING_NETWORK` / `TRADING_AUTH_FAILED` within `timeoutMs`; `subscribeTicker` keeps polling and recovers without a host restart. Tests in Tasks 4 and 5.
3. **Lots vs shares**: an order book of 10 lots of SBER (lot 10) must reach dsh-trading as `amount: 100`. Test in Tasks 1/5.
4. **Instrument with 0 or 4 decimals** (e.g. bonds, `Decimals: 4`): price strings keep exactly `Decimals` digits and parse back without float drift visible to the user. Test in Task 1.
5. **Offline mode** (seed snapshot, no live session): gateway answers with `source: "seed"`; the connector marks tickers as such in tool output so the agent does not present demo data as live. Test in Tasks 2 and 6.

---

## File Structure

| File | Responsibility |
|---|---|
| `terminal/api/gateway.go` (create) | `/api/gw/v1/*` routes, DTOs, decimal/epoch formatting |
| `terminal/api/gateway_test.go` (create) | contract tests against `newFakeFeed()` and a paper broker |
| `terminal/api/api.go` (modify `routes()`) | one line: `s.registerGateway(mux)` before the `/api/` catch-all |
| `docs/ai-terminal/gateway-v1.md` (create) | frozen JSON contract of v1 with S/O/D/I statuses |
| `dshtrading-connector-transaq/package.json` | ESM package `@local/dshtrading-connector-transaq`, exports `.` |
| `dshtrading-connector-transaq/config.js` | defaults and validation of the row config |
| `dshtrading-connector-transaq/gateway-client.js` | fetch + token + timeout + error mapping to `TRADING_*` |
| `dshtrading-connector-transaq/symbols.js` | canonical ⇄ native symbols, bare-ticker resolution |
| `dshtrading-connector-transaq/market-data.js` | `MarketDataService` implementation |
| `dshtrading-connector-transaq/trade.js` | dry-run `TradeService` + read-only paper account |
| `dshtrading-connector-transaq/tools.js` | `ru_*` tool definitions |
| `dshtrading-connector-transaq/index.js` | Cordis entry: `name`, `inject`, `apply` |
| `dshtrading-connector-transaq/test/*.test.mjs` | `node:test` suites, a fake gateway on loopback |
| `dshtrading-connector-transaq/README.md` | install into a dsh-trading profile, config table, limits |
| `Makefile` (modify) | target `connector-test` |

---

### Task 1: Gateway v1 — info and instruments

**Files:**
- Create: `terminal/api/gateway.go`, `terminal/api/gateway_test.go`, `docs/ai-terminal/gateway-v1.md`
- Modify: `terminal/api/api.go` (`routes()`, before `mux.HandleFunc("/api/", s.handleAPINotFound)`)

**Interfaces:**
- Consumes: `FeedAPI.Status()`, `FeedAPI.Instruments()`, `feed.Periods`, the max candle count from `terminal/feed/feed.go:186-205` (`maxHistoryCount`); `newTestServer`, `newFakeFeed` from `api_test.go`.
- Produces:
  - `func (s *Server) registerGateway(mux *http.ServeMux)`
  - `func formatPrice(v float64, decimals int) string`: `strconv.FormatFloat(v, 'f', decimals, 64)`; decimals < 0 → 2.
  - `func epochMS(t time.Time) int64`: 0 for zero time.
  - `GET /api/gw/v1/info` → `{"version":"1","market":"ru","provider":"finam","mode":"live|offline","connected":bool,"source":"live|seed","serverTime":ms,"trading":{"paper":true,"live":false},"intervals":[{"interval":"1m","periodSec":60,"maxBars":N},…]}`. `intervals` comes from `feed.Periods` mapped 60→`1m`, 300→`5m`, 900→`15m`, 3600→`1h`, 86400→`1d`; `maxBars` = `maxHistoryCount` (export it from `feed` as `MaxHistoryCount` if unexported).
  - `GET /api/gw/v1/instruments` → `{"instruments":[{"symbol":"TQBR:SBER","secCode":"SBER","board":"TQBR","name":…,"shortName":…,"currency":…,"decimals":2,"lotSize":10,"minStep":"0.01"}]}`

- [ ] **Step 1: Write failing tests** `TestGatewayInfo` (version `"1"`, market `ru`, five intervals in order, `trading.live == false` even when `cfg.LiveTradingAllowed`), `TestGatewayInstrumentsDecimalStrings` (fake SBER `MinStep 0.01, Decimals 2` → `"0.01"`; an instrument `Decimals 4, MinStep 0.0001` → `"0.0001"`; `Decimals 0, MinStep 1` → `"1"`), `TestGatewayRequiresToken` (with `Guard.Token` set, request without `X-Terminal-Token` → 401 `unauthorized`).
- [ ] **Step 2:** `go test ./terminal/api -run 'TestGateway' -race` → FAIL (`registerGateway` undefined / 404).
- [ ] **Step 3: Implement** `gateway.go` with the signatures above; wire `s.registerGateway(mux)` in `routes()`.
- [ ] **Step 4:** same command → PASS; then `make verify` → exit 0.
- [ ] **Step 5: Write** `docs/ai-terminal/gateway-v1.md`: header «v1 frozen; breaking change ⇒ /v2», general rules (GET-only, token, prices = decimal strings with `decimals` digits, times = epoch ms UTC, errors = existing `{"error":{"code","message"}}`), the two endpoints above with status **I** until the live check in Task 8.
- [ ] **Step 6: Commit**

```bash
git add terminal/api/gateway.go terminal/api/gateway_test.go terminal/api/api.go terminal/feed/feed.go docs/ai-terminal/gateway-v1.md
git commit -m "feat(terminal): add read-only gateway v1 info and instruments"
```

### Task 2: Gateway v1 — quotes, candles, order book, tape

**Files:** Modify `terminal/api/gateway.go`, `terminal/api/gateway_test.go`, `docs/ai-terminal/gateway-v1.md`

**Interfaces:**
- Consumes: Task 1 helpers; `FeedAPI.Instrument`, `Quote`, `Candles`, `OrderBook`, `Trades`; `s.mapError`.
- Produces (prices are strings formatted with the instrument's `decimals`; all `lots` fields are TRANSAQ lots):
  - `GET /api/gw/v1/quotes?symbols=TQBR:SBER,TQBR:GAZP` (≤ 200 symbols, else 400) → `{"quotes":{"TQBR:SBER":{"last","bid","ask","prevClose","changePct","volumeLots","time":ms,"source"}}}`. Unknown symbols are **omitted**, not errors.
  - `GET /api/gw/v1/candles?symbol=&interval=1m&limit=` → `{"symbol","interval","source","warnings":[…],"bars":[{"openTime":ms,"closeTime":ms,"open","high","low","close","volumeLots"}]}`. `closeTime = openTime + periodSec*1000 - 1`. Unknown interval → 400 `bad_request`.
  - `GET /api/gw/v1/orderbook?symbol=` → `{"symbol","time":ms,"source","bids":[{"price","lots"}],"asks":[…]}` (bids descending, asks ascending — as the feed already sorts).
  - `GET /api/gw/v1/trades?symbol=&limit=50` → `{"symbol","trades":[{"id","time":ms,"price","lots","side":"buy|sell|unknown"}]}`, ascending time. `id` = `strconv.Itoa(index)+"@"+time` when the feed carries no id (document as **I**).

- [ ] **Step 1: Confirm units.** Read `docs/protocol/records.md` and `docs/protocol/alltrades.md` for `quotes buy/sell`, `alltrades quantity`, candle `volume`, `voltoday`; record in `gateway-v1.md` which are lots, with statuses. If any is in shares, the gateway divides by `LotSize` so every `*Lots` field really is lots.
- [ ] **Step 2: Write failing tests** `TestGatewayQuotesBatchOmitsUnknown`, `TestGatewayCandlesIntervalMapping` (`1h` → feed called with 3600; `closeTime-openTime == 3_599_999`; `2h` → 400), `TestGatewayOrderBookLots` (fake level `Qty 10` → `"lots":10`, price `"306.25"`), `TestGatewayTradesSideUnknown` (`Side ""` → `"unknown"`), `TestGatewaySeedSource` (offline fake: `source == "seed"` on quotes and candles).
- [ ] **Step 3:** `go test ./terminal/api -run TestGateway -race` → FAIL.
- [ ] **Step 4: Implement** the four handlers.
- [ ] **Step 5:** tests PASS; `make verify` → exit 0.
- [ ] **Step 6: Commit** `feat(terminal): serve quotes, candles, book and tape on gateway v1`

### Task 3: Gateway v1 — read-only paper account

**Files:** Modify `terminal/api/gateway.go`, `terminal/api/gateway_test.go`, `docs/ai-terminal/gateway-v1.md`

**Interfaces:**
- Consumes: `PaperAPI.Snapshot()`; `paper.Status*` constants.
- Produces: `GET /api/gw/v1/account` → `{"environment":"paper","currency":"RUB","cash":"…","positions":[{"symbol","qty","avgPrice","realizedPnl"}],"openOrders":[{"id","symbol","side","type","status","qty","filled","price","time":ms}],"fills":[{"id","orderId","symbol","side","qty","price","fee","time":ms}]}`. `qty` is in **shares** (paper cash moves by `qty*price`); `openOrders` only `new`/`partial`; status mapping `partial→partially_filled`, `cancelled→canceled`, others unchanged; `fills` ascending time, last 50. `Deps.Paper == nil` → 503 `unavailable`.

- [ ] **Step 1: Write failing tests** `TestGatewayAccountMapsStatuses` (place a resting limit order and a filled one through a real `paper.Broker`; open orders contain only the resting one with `"status":"new"`), `TestGatewayAccountNoPaper` (503).
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS + `make verify`.
- [ ] **Step 5: Commit** `feat(terminal): expose read-only paper account on gateway v1`

### Task 4: Connector skeleton — config, gateway client, symbols

**Files:** Create `dshtrading-connector-transaq/{package.json,config.js,gateway-client.js,symbols.js}`, `test/{gateway-client,symbols}.test.mjs`, `test/fake-gateway.mjs`; modify `Makefile`.

**Interfaces:**
- Produces:
  - `config.js`: `export const DEFAULTS = { enabled: true, gatewayUrl: 'http://127.0.0.1:18090', tokenRef: 'TERMINAL_API_TOKEN', timeoutMs: 5000, pollMs: 2000 }`; `export function resolveConfig(raw) → Config` (merge DEFAULTS; `gatewayUrl` must be `http://` loopback (`127.0.0.1`, `localhost`, `[::1]`), else throw `TypeError`; `tokenRef` must match `/^[A-Za-z_][A-Za-z0-9_]*$/`).
  - `gateway-client.js`: `export class TradingError extends Error { code }` with `name = 'TradingServiceError'`; `export function createGatewayClient({ config, resolveToken, fetchImpl = fetch }) → { get(path, query, signal) → Promise<object> }`. Sends `X-Terminal-Token` when a token resolves. Mapping: 401 → `TRADING_AUTH_FAILED`; 404 → `TRADING_SYMBOL_NOT_FOUND`; 400 → `TRADING_UNSUPPORTED_SYMBOL`; 503 and other non-2xx → `TRADING_UPSTREAM_ERROR`; fetch rejection or `AbortSignal.timeout(timeoutMs)` → `TRADING_NETWORK`. Messages carry the gateway `error.message`, never the token.
  - `symbols.js`: `toNative('SBER.TQBR') → 'TQBR:SBER'`, `toCanonical('TQBR:SBER') → 'SBER.TQBR'`; `createSymbolResolver(loadInstruments) → { resolve(input) → Promise<{native, canonical, instrument}> }`. Accepts canonical, native or bare `SECCODE` (case-insensitive). Bare: unique match → it; several → `TQBR` if present, else `TRADING_UNSUPPORTED_SYMBOL` listing candidates; none → `TRADING_SYMBOL_NOT_FOUND`. The instrument list is cached for 60 s and refreshed once on a miss.
  - `test/fake-gateway.mjs`: `startFakeGateway({ token, routes }) → { url, close, calls }`, a loopback `node:http` server returning canned v1 JSON.
  - `Makefile`: `connector-test: ; node --test dshtrading-connector-transaq/test/`

- [ ] **Step 1: Write failing tests** `resolveConfig rejects non-loopback gatewayUrl`, `client maps 401/404/503/timeout to TRADING_* codes`, `client error never contains the token`, `resolver: SBER → TQBR:SBER, sber.tqbr → TQBR:SBER, ambiguous without TQBR lists candidates, unknown → TRADING_SYMBOL_NOT_FOUND, miss refreshes cache once`.
- [ ] **Step 2:** `make connector-test` → FAIL (modules missing).
- [ ] **Step 3: Implement** the three modules and `package.json` (`"type":"module"`, `"private":true`, `"license"` left for the owner's decision with `"UNLICENSED"` placeholder, `"engines":{"node":">=24"}`, `"exports":{".":"./index.js"}`).
- [ ] **Step 4:** `make connector-test` → PASS.
- [ ] **Step 5: Commit** `feat(connector-transaq): add gateway client, config and symbol vocabulary`

### Task 5: Connector MarketDataService

**Files:** Create `dshtrading-connector-transaq/market-data.js`, `test/market-data.test.mjs`

**Interfaces:**
- Consumes: Task 4 `createGatewayClient`, `createSymbolResolver`, `TradingError`.
- Produces: `export function createMarketDataService({ client, resolver, pollMs }) → MarketDataService` with:
  - `getTicker(symbol) → Ticker` (`symbol` canonical, `name` = shortName‖name, `price/bid/ask/prevClose` = `Number(str)`, `changePercent`, `volume = volumeLots*lotSize`, `timestamp`); missing quote → `TRADING_SYMBOL_NOT_FOUND`.
  - `getTickers(symbols) → Record<input, Ticker>` (one `/quotes` call, keys = original input strings, unresolvable inputs omitted).
  - `getKlines(symbol, interval, limit=500) → Kline[]` (interval not in `/info` → `TRADING_UNSUPPORTED_INTERVAL` without calling `/candles`; `volume = volumeLots*lotSize`).
  - `klineCapabilities() → [{interval, maxBars}]` from `/info`, cached.
  - `listInstruments() → [{symbol: canonical, name, form: 'spot', assetClass: 'equity'}]`.
  - `getOrderbook(symbol) → Orderbook` (`amount = lots*lotSize`), `getRecentTrades(symbol, limit) → TradeTick[]`.
  - `subscribeTicker(symbol, cb) → { dispose() }`: polls `getTicker` every `pollMs`, calls `cb` only when `price` or `timestamp` changed, swallows errors between polls, stops on `dispose`.

- [ ] **Step 1: Write failing tests** `getTicker converts lots and canonical symbol` (SBER lot 10, `volumeLots 5` → `volume 50`, `symbol 'SBER.TQBR'`), `getTickers keeps input keys and omits unknown`, `getKlines rejects 2h before network` (fake gateway `calls` has no `/candles`), `orderbook amount is lots*lotSize`, `subscribeTicker recovers after gateway restart` (fake returns 503 twice then 200; `cb` called once, then `dispose` stops calls).
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** `make connector-test` → PASS.
- [ ] **Step 5: Commit** `feat(connector-transaq): implement MarketDataService over gateway v1`

### Task 6: Connector TradeService (dry-run + paper views) and ru_* tools

**Files:** Create `dshtrading-connector-transaq/trade.js`, `tools.js`, `test/trade.test.mjs`, `test/tools.test.mjs`

**Interfaces:**
- Consumes: Tasks 4–5.
- Produces:
  - `export function createTradeService({ client, resolver }) → TradeService`:
    - `placeOrder(req)`: `dryRun !== false` → validate (resolved symbol; `quantity` > 0 and multiple of `lotSize`; limit requires `price` that is a multiple of `minStep`, compared on decimal strings scaled to `decimals`) and return `{ id: 'dry-' + randomUUID(), symbol: canonical, side, type, status: 'new', price, quantity, filledQuantity: 0, dryRun: true, timestamp: Date.now() }`; validation failure → `TRADING_EXCHANGE_ERROR` with the reason. `dryRun === false` → `TRADING_LIVE_TRADING_DISABLED` (no network call).
    - `cancelOrder(id)`: `id` starts with `dry-` → resolves; otherwise `TRADING_LIVE_TRADING_DISABLED`.
    - `getOrder(symbol, id)`: from `/account` `openOrders`, else `TRADING_SYMBOL_NOT_FOUND`.
    - `getPositions()` (`side` `long`/`short` by sign, `quantity` = |qty|), `getBalances()` (`[{asset:'RUB', free: Number(cash), …}]`, field set per upstream `AccountBalance`), `listOpenOrders(symbol?)`, `listTradeFills(symbol?, limit=50)`; symbols canonical.
  - `tools.js`: `export function buildTools({ market, trade }) → ToolDefinition[]` — `ru_get_ticker`, `ru_get_klines`, `ru_get_orderbook`, `ru_get_trades`, `ru_list_instruments`, `ru_get_paper_account`, each `{ name, description, parameters (JSON Schema), execute(args, exec) }` (same plain shape as `dsh-ai-terminal/tools.js:297`). Descriptions in English, state units (shares, RUB) and that data may be `source: seed` (offline demo); results include `source`.

- [ ] **Step 1: Write failing tests** `placeOrder defaults to dry-run and validates lot multiple` (SBER lot 10: qty 15 → `TRADING_EXCHANGE_ERROR`, qty 20 → dry receipt `dryRun: true`), `limit price off-step rejected (306.255 with step 0.01)`, `dryRun false is rejected without network`, `positions map sign to side`, `buildTools has no *_place_order or *_cancel_order`, `ru_get_ticker result carries source seed in offline fake`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(connector-transaq): add dry-run trade service and ru tools`

### Task 7: Cordis entry and registration

**Files:** Create `dshtrading-connector-transaq/index.js`, `test/apply.test.mjs`

**Interfaces:**
- Consumes: everything above.
- Produces: `export const name = 'dsh-trading-ru-connector-transaq'`; `export const inject = []`; `export function apply(ctx, rawConfig)`:
  1. `config = resolveConfig(rawConfig)`; `!config.enabled` → return.
  2. `resolveToken` = `ctx.get?.('credentials', false)?.resolve(config.tokenRef)` value, falling back to `process.env[config.tokenRef]`, resolved on each request.
  3. If `ctx.get?.('tradingMarketDataRegistry', false)` exists → `ctx.effect(() => registry.register('ru', 'finam', market))`; same for `tradingTradeRegistry` with the trade service.
  4. If `ctx.tools?.register` exists → register every tool from `buildTools`; dispose each through `ctx.effect` when `register` returns a disposer.
  5. Neither registry nor tools → `ctx.logger?.('transaq')?.warn(...)` once, no throw.

- [ ] **Step 1: Write failing tests** with a fake ctx `{ get, effect, tools, logger }`: `registers ru/finam in both registries`, `enabled:false registers nothing`, `missing registries still registers tools`, `token resolved per request (rotate env between two calls → second call sends new token)`.
- [ ] **Step 2:** FAIL. **Step 3:** implement. **Step 4:** `make connector-test` → PASS.
- [ ] **Step 5: Commit** `feat(connector-transaq): wire Cordis apply and registry registration`

### Task 8: README, acceptance in a real dsh-trading host

**Files:** Create `dshtrading-connector-transaq/README.md`; modify `docs/ai-terminal/gateway-v1.md` (statuses), `docs/ai-terminal/adr-dsh-trading.md` (status line), `docs/ai-terminal/PROGRESS.md`.

- [ ] **Step 1: Write README:** what it is; prerequisites (`bin/terminal` running with `TERMINAL_API_TOKEN`); the profile row

```yaml
- id: dsh-trading-ru-connector-transaq
  name: '@local/dshtrading-connector-transaq'
  config: { enabled: true, gatewayUrl: 'http://127.0.0.1:18090', tokenRef: TERMINAL_API_TOKEN }
```

  config table (Task 4 `DEFAULTS`); limits: no `ru` tab in dsh-trading GUI until upstream opens its market list (ADR §«Что важно» п.3), no live/paper order placement, agent reaches MOEX only through `ru_*` tools.
- [ ] **Step 2: Offline acceptance (no user consent needed):** `make terminal && TERMINAL_API_TOKEN=… bin/terminal -mode offline -addr 127.0.0.1:18090`, then `node --input-type=module -e` script that `apply()`s the connector with a fake ctx and prints `getTicker('SBER')`, `getKlines('SBER','1h',5)`, `getOrderbook('SBER')`. Expected: canonical `SBER.TQBR`, `source: seed`, non-empty bars.
- [ ] **Step 3: Host acceptance — ask the user first** (installs dsh-trading packages from npm into a separate `DSH_HOME=~/.dsh-trading`). Install per their README, add the row via `plugin_manager`, run their `trading-web`, ask the agent «ru_get_ticker SBER»; screenshot to `docs/ai-terminal/screenshot-dsh-trading-ru.png`. If not run, write «не проверено в хосте dsh-trading» in PROGRESS.
- [ ] **Step 4: Live read-only check (demo server window 09:50–23:50 МСК, consent per AGENTS.md §6):** `-mode live` with `~/re/demo.env`; repeat Step 2; flip statuses in `gateway-v1.md` from **I** to **S** only for what was observed.
- [ ] **Step 5:** `make verify && make connector-test` → exit 0.
- [ ] **Step 6: Commit** `docs(connector-transaq): document install and acceptance of the dsh-trading connector`

---

## Out of scope (separate plans)

- Upstream issue/PR to make dsh-trading's market list open (ADR item 4).
- Clean-room ports to Go: `intentId` ledger, authority plane, gates (ADR item 5).
- Choosing and adding a LICENSE (ADR item 1) — owner's decision; blocks npm publishing, not this plan.
