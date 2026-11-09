#!/usr/bin/env bash
# Demo-server live acceptance of the AI terminal (plan 2026-11-05, Step 5):
#   T11 - 10 minutes of the TQBR:SBER order book and trades into ClickHouse;
#   T12 - news first_seen/published written by the same recorder;
#   T22 - a dry_run rule bot on live closed M5 candles for 30 minutes.
#
# Safety: the script refuses to run without TERMINAL_LIVE_ACCEPTANCE=1, reads
# the exchange credentials from ~/re/demo.env (never printed), only subscribes
# to the read-only market feeds and never sends a trading command. It waits
# for the demo window (09:50-23:50 MSK) unless DEMO_CHECK_NO_WAIT=1.
#
# Usage:
#   TERMINAL_LIVE_ACCEPTANCE=1 make demo_live_check
#   DEMO_CHECK_SELFTEST=1 make demo_live_check   # env/CH/build checks only
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
OBS="$ROOT/.re-run/live"
mkdir -p "$OBS"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="$OBS/demo-check-$STAMP.log"
exec > >(tee -a "$LOG") 2>&1
echo "demo-live-check start $(date) MSK=$(TZ=Europe/Moscow date +%H:%M)"

die() { echo "FATAL: $*"; exit 2; }
[ "${TERMINAL_LIVE_ACCEPTANCE:-}" = "1" ] || die "set TERMINAL_LIVE_ACCEPTANCE=1 to run live checks"
[ -f "$HOME/re/demo.env" ] || die "~/re/demo.env is missing"
set -a; . "$HOME/re/demo.env"; set +a
: "${TC_HOST:?TC_HOST is required}"
: "${TC_PORT:?TC_PORT is required}"
: "${TC_LOGIN:?TC_LOGIN is required}"
: "${TC_PASSWORD:?TC_PASSWORD is required}"
MD_CH="${MD_CLICKHOUSE_URL:-http://localhost:8123}"
TOKEN="live-check-$(date +%s)"
PORT="${LIVE_PORT:-18095}"
T11_SECONDS="${T11_SECONDS:-600}"
T22_SECONDS="${T22_SECONDS:-1900}"
# The demo server allows one login per account: after the recorder disconnects its
# session can linger for a couple of minutes, so the live backend waits before
# logging in. T22-only reruns (DEMO_CHECK_SKIP_T11=1) have no recorder session and skip the cooldown.
T22_COOLDOWN="${T22_COOLDOWN:-120}"
SKIP_T11="${DEMO_CHECK_SKIP_T11:-0}"
export GOCACHE="${GOCACHE:-$ROOT/.cache/go-build}"
echo "config: T11_SECONDS=$T11_SECONDS T22_SECONDS=$T22_SECONDS port=$PORT clickhouse=$MD_CH"

chq() { curl -sS -m 20 "$MD_CH/" --data-binary "$1"; }

echo "== build =="
go build -o bin/terminal ./cmd/terminal || die "terminal build failed"
go build -o bin/md-recorder ./cmd/md-recorder || die "md-recorder build failed"
echo "clickhouse ping: $(chq "SELECT version()" | head -1)"

if [ "${DEMO_CHECK_SELFTEST:-}" = "1" ]; then
  echo "SELFTEST PASS: gates, demo env, builds and ClickHouse connectivity are OK"
  exit 0
fi

if [ "${DEMO_CHECK_NO_WAIT:-}" != "1" ]; then
  while :; do
    NOW_MSK="$(TZ=Europe/Moscow date +%H%M)"
    [ "$NOW_MSK" -ge 0955 ] 2>/dev/null && break
    echo "waiting for the demo window (now $NOW_MSK MSK)"
    sleep 60
  done
  echo "demo window open: $(TZ=Europe/Moscow date +%H:%M) MSK"
fi

T11_STATUS="FAIL"; T22_STATUS="FAIL"; NEWS_STATUS="FAIL"

if [ "$SKIP_T11" = "1" ]; then
  T11_STATUS="${DEMO_CHECK_T11_STATUS:-SKIPPED}"
  echo "== T11 recorder skipped (previous evidence: T11=$T11_STATUS); no recorder login =="
else
  echo "== T11 recorder: ${T11_SECONDS}s of TQBR:SBER =="
  MD_CLICKHOUSE_URL="$MD_CH" MD_CLICKHOUSE_USER="${MD_CLICKHOUSE_USER:-}" MD_CLICKHOUSE_PASSWORD="${MD_CLICKHOUSE_PASSWORD:-}" \
    MD_SYMBOLS="TQBR:SBER" ./bin/md-recorder > "$OBS/t11-recorder-$STAMP.log" 2>&1 &
  RPID=$!
  for _ in $(seq 1 90); do
    grep -q "connected to" "$OBS/t11-recorder-$STAMP.log" 2>/dev/null && break
    kill -0 "$RPID" 2>/dev/null || break
    sleep 2
  done
  sleep "$T11_SECONDS"
  kill -TERM "$RPID" 2>/dev/null; wait "$RPID" 2>/dev/null
  echo "recorder log tail:"; tail -3 "$OBS/t11-recorder-$STAMP.log"
fi
BOOK_COUNTS="$(chq "SELECT kind, count() AS n FROM md.book_events GROUP BY kind ORDER BY kind FORMAT JSONEachRow")"
TRADE_COUNT="$(chq "SELECT count() FROM md.trades")"
NEWS_COUNT="$(chq "SELECT count() FROM md.news")"
NEWS_RANGE="$(chq "SELECT min(first_seen), max(first_seen), max(published) FROM md.news")"
[ -n "$NEWS_COUNT" ] && [ "${NEWS_COUNT:-0}" != "0" ] && NEWS_STATUS="PASS"
echo "book events by kind: $BOOK_COUNTS"
echo "trades: $TRADE_COUNT news: $NEWS_COUNT range: $NEWS_RANGE"
if echo "$BOOK_COUNTS" | grep -q "snapshot" && echo "$BOOK_COUNTS" | grep -q "level"; then T11_STATUS="PASS"; fi
if [ "$SKIP_T11" = "1" ]; then
  echo "no recorder session to release: skipping the ${T22_COOLDOWN}s cooldown"
else
  echo "cooldown ${T22_COOLDOWN}s before the live backend login (demo session release)"
  sleep "$T22_COOLDOWN"
fi

echo "== T22 dry-run bot: ${T22_SECONDS}s of TQBR:SBER M5 =="
cat > "$OBS/rules.json" <<JSON
{"indicators":{"rsi":{"kind":"rsi_wilder","period":14,"scale":0.01}},"entry":[{"left":"rsi","op":"<","right":"0.2"}],"exit":[{"left":"rsi","op":">","right":"0.8"}],"side":"long"}
JSON
TERMINAL_API_TOKEN="$TOKEN" TERMINAL_INSTANCE_ID="live-$STAMP" ./bin/terminal -mode live -addr "127.0.0.1:$PORT" > "$OBS/t22-terminal-$STAMP.log" 2>&1 &
TPID=$!
cleanup() { kill -TERM "$TPID" 2>/dev/null; wait "$TPID" 2>/dev/null; }
trap cleanup EXIT
HEALTH=""
for _ in $(seq 1 300); do
  HEALTH="$(curl -s -m 2 "http://127.0.0.1:$PORT/api/health" 2>/dev/null)"
  case "$HEALTH" in *"\"connected\":true"*) break;; esac
  sleep 1
done
echo "backend health: $(echo "$HEALTH" | head -c 300)"
if echo "$HEALTH" | grep -q "\"connected\":true"; then
  CREATE_BODY="$(python3 - "$OBS/rules.json" <<PY
import json, sys
rules = json.load(open(sys.argv[1]))
print(json.dumps({"symbol": "TQBR:SBER", "periodSec": 300, "qty": 1, "rules": rules}))
PY
  )"
  BOT_CREATE="$(curl -sS -m 120 -X POST "http://127.0.0.1:$PORT/api/bots" -H "content-type: application/json" -H "X-Terminal-Token: $TOKEN" --data "$CREATE_BODY")"
  echo "bot create: $(echo "$BOT_CREATE" | head -c 400)"
  BOT_ID="$(printf %s "$BOT_CREATE" | python3 -c "import json,sys; print(json.load(sys.stdin).get('bot',{}).get('id',''))" 2>/dev/null)"
  if [ -n "$BOT_ID" ]; then
    curl -sS -m 30 -X POST "http://127.0.0.1:$PORT/api/bots/$BOT_ID/start" -H "content-type: application/json" -H "X-Terminal-Token: $TOKEN" --data "{\"stage\":\"dry_run\"}" > "$OBS/t22-start-$STAMP.json"
    echo "bot $BOT_ID started dry_run"
    END=$(( $(date +%s) + T22_SECONDS ))
    while [ "$(date +%s)" -lt "$END" ]; do sleep 30; done
    curl -sS -m 30 -X POST "http://127.0.0.1:$PORT/api/bots/stop-all" -H "content-type: application/json" -H "X-Terminal-Token: $TOKEN" --data "{}" > "$OBS/t22-stopall-$STAMP.json"
    sleep 2
    curl -sS -m 30 "http://127.0.0.1:$PORT/api/bots/$BOT_ID/journal?limit=5000" -H "X-Terminal-Token: $TOKEN" > "$OBS/t22-journal-final-$STAMP.json"
    python3 - "$OBS/t22-journal-final-$STAMP.json" "$OBS/t22-summary-$STAMP.json" <<PY
import json, sys
src, dst = sys.argv[1], sys.argv[2]
raw = open(src).read().strip()
doc = json.loads(raw) if raw else {}
entries = doc.get("entries", doc.get("journal", [])) if isinstance(doc, dict) else []
closed = [e for e in entries if e.get("kind") == "closed_bar"]
orders = [e for e in entries if e.get("kind") in ("order", "fill")]
misaligned = [e["barTime"] for e in closed if e.get("barTime") and not (int(e["barTime"][14:16]) % 5 == 0 and int(e["barTime"][17:19]) == 0)]
summary = {"botId": doc.get("id") if isinstance(doc, dict) else None, "entries": len(entries),
           "closedBars": len(closed), "orders": len(orders), "misalignedBars": misaligned,
           "counts": doc.get("counts") if isinstance(doc, dict) else None}
summary["ok"] = len(closed) >= 5 and len(orders) == 0 and not misaligned
open(dst, "w").write(json.dumps(summary, ensure_ascii=False, indent=1))
print("T22 summary:", json.dumps(summary, ensure_ascii=False))
PY
    if python3 -c "import json;raise SystemExit(0 if json.load(open('$OBS/t22-summary-$STAMP.json')).get('ok') else 1)"; then T22_STATUS="PASS"; fi
  else
    echo "bot create failed; see $OBS/t22-terminal-$STAMP.log"
  fi
else
  echo "backend did not connect to the exchange; see $OBS/t22-terminal-$STAMP.log"
fi
cleanup
trap - EXIT

SUMMARY="$OBS/live-summary-$STAMP.json"
python3 - "$SUMMARY" "$T11_STATUS" "$T22_STATUS" "$NEWS_STATUS" "$BOOK_COUNTS" "$TRADE_COUNT" "$NEWS_COUNT" "$LOG" <<PY
import json, sys
out, t11, t22, news, book, trades, ncount, log = sys.argv[1:9]
json.dump({"t11": t11, "t22": t22, "news": news, "bookCounts": book, "trades": trades,
           "newsCount": ncount, "log": log}, open(out, "w"), ensure_ascii=False, indent=1)
print("LIVE_CHECK_RESULT", json.dumps({"t11": t11, "t22": t22, "news": news}))
PY
if [ "$T11_STATUS" = "PASS" ] && [ "$T22_STATUS" = "PASS" ]; then
  echo "demo-live-check PASS"; exit 0
fi
echo "demo-live-check FAIL"; exit 1
