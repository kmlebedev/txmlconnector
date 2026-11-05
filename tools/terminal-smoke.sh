#!/usr/bin/env bash
# terminal-smoke.sh — end-to-end offline acceptance smoke of the AI terminal.
#
# Builds bin/terminal, starts it in offline mode (bundled seed, loopback only,
# no network/DLL/credentials), waits for /api/health and verifies the frozen
# API surface: token guard, market data, backtest, agent tools + invoke, rule
# bots (create/list/check), MCP JSON-RPC (/mcp), SSE /api/events and the graceful
# SIGTERM shutdown. Every step prints PASS/FAIL; the exit code is non-zero when
# at least one check fails.
#
# Usage:
#   tools/terminal-smoke.sh
#   make terminal_smoke
#
# Environment overrides:
#   SMOKE_BIN=...        binary path (default: bin/terminal)
#   SMOKE_SKIP_BUILD=1   reuse an existing binary
#   SMOKE_PORT=...       fixed port (default: a free loopback port)
#   SMOKE_TOKEN=...      test API token (default: a random test token)
#   SMOKE_KEEP=1         keep the temp dir with the backend log on exit
#   SMOKE_HTTP_TIMEOUT=  per-request curl timeout in seconds (default: 30)

set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT" || { echo "FAIL  cannot enter the repo root"; exit 1; }

GOCACHE="$REPO_ROOT/.cache/go-build"
export GOCACHE

if [ -n "$SMOKE_BIN" ]; then
  BIN="$SMOKE_BIN"
else
  BIN="$REPO_ROOT/bin/terminal"
fi
if [ -n "$SMOKE_HTTP_TIMEOUT" ]; then
  HTTP_TIMEOUT="$SMOKE_HTTP_TIMEOUT"
else
  HTTP_TIMEOUT=30
fi

PASS=0
FAIL=0
pass() {
  PASS=$((PASS + 1))
  printf 'PASS  %s\n' "$1"
}
fail() {
  FAIL=$((FAIL + 1))
  printf 'FAIL  %s' "$1"
  if [ -n "$2" ]; then
    printf ' — %s' "$2"
  fi
  printf '\n'
}
check_status() {
  if [ "$2" = "$3" ]; then
    pass "$1"
  else
    fail "$1" "expected HTTP $2, got $3"
  fi
}
check_eq() {
  if [ "$2" = "$3" ]; then
    pass "$1"
  else
    fail "$1" "expected \"$2\", got \"$3\""
  fi
}
check_ge() {
  if [ "$3" -ge "$2" ] 2>/dev/null; then
    pass "$1"
  else
    fail "$1" "expected >= $2, got \"$3\""
  fi
}

TMP="$(mktemp -d -t terminal-smoke)"
LOG="$TMP/terminal.log"
BODY="$TMP/body.json"
SSE_FILE="$TMP/sse.txt"
SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -TERM "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
  fi
  if [ "$SMOKE_KEEP" = "1" ]; then
    echo "== kept temp dir: $TMP"
  else
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT INT TERM

# json_get <file> <dotted.path> — prints a scalar or compact JSON, empty when absent.
cat > "$TMP/jsonhelp.py" <<'PY'
import json
import sys

mode = sys.argv[1]
path = sys.argv[2]
with open(path, 'r', encoding='utf-8') as handle:
    data = json.load(handle)

def resolve(value, dotted):
    current = value
    for part in dotted.split('.'):
        if part == '':
            continue
        if isinstance(current, list):
            index = int(part)
            if index < 0 or index >= len(current):
                return None
            current = current[index]
        elif isinstance(current, dict):
            if part not in current:
                return None
            current = current[part]
        else:
            return None
    return current

if mode == 'get':
    value = resolve(data, sys.argv[3])
    if value is None:
        sys.exit(0)
    if isinstance(value, bool):
        print('true' if value else 'false')
    elif isinstance(value, (int, float, str)):
        print(value)
    else:
        print(json.dumps(value, ensure_ascii=False))
elif mode == 'len':
    value = resolve(data, sys.argv[3])
    if isinstance(value, (list, dict)):
        print(len(value))
    else:
        print(0)
elif mode == 'find':
    needle = sys.argv[3]
    value = resolve(data, sys.argv[4])
    found = 'no'
    if isinstance(value, list):
        for item in value:
            if isinstance(item, dict) and item.get('name') == needle:
                found = 'yes'
                break
    print(found)
PY
json_get() {
  python3 "$TMP/jsonhelp.py" get "$1" "$2" 2>/dev/null
}
json_len() {
  python3 "$TMP/jsonhelp.py" len "$1" "$2" 2>/dev/null
}
json_find() {
  python3 "$TMP/jsonhelp.py" find "$1" "$2" "$3" 2>/dev/null
}

# req <method> <path> <token|""> [json-body] — sets STATUS, leaves the body in BODY.
req() {
  local method="$1"
  local path="$2"
  local token="$3"
  local data="$4"
  local url="$BASE$path"
  if [ "$method" = "GET" ]; then
    if [ -n "$token" ]; then
      STATUS=$(curl -sS -m "$HTTP_TIMEOUT" -o "$BODY" -w '%{http_code}' -H "X-Terminal-Token: $token" "$url" 2>/dev/null)
    else
      STATUS=$(curl -sS -m "$HTTP_TIMEOUT" -o "$BODY" -w '%{http_code}' "$url" 2>/dev/null)
    fi
  else
    if [ -n "$token" ] && [ -n "$data" ]; then
      STATUS=$(curl -sS -m "$HTTP_TIMEOUT" -o "$BODY" -w '%{http_code}' -X "$method" \
        -H "X-Terminal-Token: $token" -H 'Content-Type: application/json' \
        --data-binary "$data" "$url" 2>/dev/null)
    elif [ -n "$token" ]; then
      STATUS=$(curl -sS -m "$HTTP_TIMEOUT" -o "$BODY" -w '%{http_code}' -X "$method" \
        -H "X-Terminal-Token: $token" "$url" 2>/dev/null)
    elif [ -n "$data" ]; then
      STATUS=$(curl -sS -m "$HTTP_TIMEOUT" -o "$BODY" -w '%{http_code}' -X "$method" \
        -H 'Content-Type: application/json' --data-binary "$data" "$url" 2>/dev/null)
    else
      STATUS=$(curl -sS -m "$HTTP_TIMEOUT" -o "$BODY" -w '%{http_code}' -X "$method" "$url" 2>/dev/null)
    fi
  fi
  if [ -z "$STATUS" ]; then
    STATUS=000
  fi
}

abort() {
  echo
  echo "SMOKE FAIL (fatal): $1"
  if [ -f "$LOG" ]; then
    echo '--- backend log (tail) ---'
    tail -25 "$LOG" 2>/dev/null
  fi
  exit 1
}

echo "== AI terminal offline smoke"
echo "   repo: $REPO_ROOT"
echo "   bin:  $BIN"

if [ "$SMOKE_SKIP_BUILD" = "1" ]; then
  echo "== reuse existing binary (SMOKE_SKIP_BUILD=1)"
  if [ -x "$BIN" ]; then
    pass "reuse existing binary ($BIN)"
  else
    fail "reuse existing binary" "not executable: $BIN"
    abort "SMOKE_SKIP_BUILD=1 requires an executable SMOKE_BIN"
  fi
else
  echo "== build ./cmd/terminal"
  mkdir -p "$(dirname "$BIN")"
  if go build -o "$BIN" ./cmd/terminal; then
    pass "build bin/terminal"
  else
    fail "build bin/terminal"
    abort "go build ./cmd/terminal failed"
  fi
fi
if [ ! -x "$BIN" ]; then
  abort "binary is not executable: $BIN"
fi

if [ -n "$SMOKE_PORT" ]; then
  PORT="$SMOKE_PORT"
else
  PORT=$(python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()' 2>/dev/null)
fi
if [ -z "$PORT" ]; then
  abort "cannot allocate a free loopback port"
fi
BASE="http://127.0.0.1:$PORT"

if [ -n "$SMOKE_TOKEN" ]; then
  TOKEN="$SMOKE_TOKEN"
else
  TOKEN="terminal-smoke-$(date +%s)-$RANDOM"
fi
INSTANCE_ID="terminal-smoke-instance-$(date +%s)-$RANDOM"
echo "   port: $PORT"
echo "   token: test-only (X-Terminal-Token)"

TERMINAL_API_TOKEN="$TOKEN" TERMINAL_INSTANCE_ID="$INSTANCE_ID" \
  "$BIN" -mode offline -addr "127.0.0.1:$PORT" >"$LOG" 2>&1 &
SERVER_PID=$!

READY=0
for _ in $(seq 1 150); do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' -m 1 "$BASE/api/health" 2>/dev/null)
  if [ "$CODE" = "200" ]; then
    READY=1
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    break
  fi
  sleep 0.2
done
if [ "$READY" != "1" ]; then
  fail "start offline backend"
  abort "backend did not become healthy on $BASE/api/health"
fi
pass "start offline backend (port $PORT)"
if grep -q 'режим=offline' "$LOG" 2>/dev/null; then
  pass "offline mode reported in the log"
else
  fail "offline mode reported in the log"
fi

echo "== token guard"
req GET /api/health ""
check_status "/api/health without token" 200 "$STATUS"
HEALTH_INSTANCE=$(json_get "$BODY" instanceId)
check_eq "/api/health instanceId" "$INSTANCE_ID" "$HEALTH_INSTANCE"

req GET /api/instruments ""
check_status "/api/instruments without token" 401 "$STATUS"
req GET /api/events ""
check_status "/api/events without token" 401 "$STATUS"
MCP_NO_TOKEN='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}'
req POST /mcp "" "$MCP_NO_TOKEN"
check_status "/mcp without token" 401 "$STATUS"

echo "== market data"
req GET /api/instruments "$TOKEN"
check_status "/api/instruments with token" 200 "$STATUS"
INSTRUMENTS_COUNT=$(json_len "$BODY" instruments)
check_ge "/api/instruments count" 1 "$INSTRUMENTS_COUNT"
SYMBOL=$(python3 - "$BODY" <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as handle:
    data = json.load(handle)
items = data.get('instruments') or []
symbol = ''
for item in items:
    candidate = item.get('symbol') or ''
    if candidate:
        symbol = candidate
        break
print(symbol)
PY
)
if [ -n "$SYMBOL" ]; then
  pass "pick an instrument symbol ($SYMBOL)"
else
  fail "pick an instrument symbol"
  abort "no symbol in /api/instruments"
fi

echo "== backtest"
BT_DATA=$(printf '{"symbol":"%s","periodSec":300,"strategy":"sma_cross","count":300}' "$SYMBOL")
req POST /api/backtest "$TOKEN" "$BT_DATA"
check_status "POST /api/backtest" 200 "$STATUS"
BT_BARS=$(json_get "$BODY" result.metrics.bars)
check_ge "backtest bars" 1 "$BT_BARS"
BT_STRATEGY=$(json_get "$BODY" result.strategy)
check_eq "backtest strategy" "sma_cross" "$BT_STRATEGY"

echo "== agent tools"
req GET /api/agent/tools "$TOKEN"
check_status "GET /api/agent/tools" 200 "$STATUS"
TOOLS_COUNT=$(json_len "$BODY" tools)
check_ge "agent tools count" 30 "$TOOLS_COUNT"
HAS_QUOTE=$(json_find "$BODY" get_quote tools)
check_eq "agent tools contains get_quote" "yes" "$HAS_QUOTE"

QUOTE_ARGS=$(printf '{"args":{"symbol":"%s"}}' "$SYMBOL")
req POST "/api/agent/tools/get_quote/invoke" "$TOKEN" "$QUOTE_ARGS"
check_status "POST invoke get_quote" 200 "$STATUS"
INVOKE_SYMBOL=$(json_get "$BODY" result.symbol)
check_eq "invoke result symbol" "$SYMBOL" "$INVOKE_SYMBOL"
INVOKE_ERROR=$(json_get "$BODY" error)
if [ -z "$INVOKE_ERROR" ]; then
  pass "invoke result has no tool error"
else
  fail "invoke result has no tool error" "$INVOKE_ERROR"
fi

echo "== rule bots"
BOT_DATA=$(printf '{"symbol":"%s","periodSec":300,"qty":1,"rules":{"indicators":{"rsi":{"kind":"rsi_wilder","period":14,"scale":0.01}},"entry":[{"left":"rsi","op":"<","right":"0.2"}],"exit":[{"left":"rsi","op":">","right":"0.8"}]}}' "$SYMBOL")
req POST /api/bots "$TOKEN" "$BOT_DATA"
check_status "POST /api/bots (create from rules.Spec)" 201 "$STATUS"
BOT_ID=$(json_get "$BODY" bot.id)
BOT_STAGE=$(json_get "$BODY" bot.stage)
if [ -n "$BOT_ID" ]; then
  pass "created bot id ($BOT_ID)"
else
  fail "created bot id"
  abort "no bot id in POST /api/bots response"
fi
check_eq "created bot stage" "check" "$BOT_STAGE"

req GET /api/bots "$TOKEN"
check_status "GET /api/bots" 200 "$STATUS"
BOTS_COUNT=$(json_len "$BODY" bots)
check_ge "bots list count" 1 "$BOTS_COUNT"

req POST "/api/bots/$BOT_ID/check" "$TOKEN" ""
check_status "POST /api/bots/{id}/check" 200 "$STATUS"
CHECK_KIND=$(json_get "$BODY" entry.kind)
check_eq "bot check journal kind" "closed_bar" "$CHECK_KIND"
CHECK_MESSAGE=$(json_get "$BODY" entry.message)
case "$CHECK_MESSAGE" in
  *"signal="*) pass "bot check message has close/signal" ;;
  *) fail "bot check message has close/signal" "$CHECK_MESSAGE" ;;
esac

echo "== MCP"
MCP_INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"terminal-smoke","version":"1.0"}}}'
req POST /mcp "$TOKEN" "$MCP_INIT"
check_status "POST /mcp initialize" 200 "$STATUS"
MCP_SERVER=$(json_get "$BODY" result.serverInfo.name)
check_eq "MCP server name" "ai-terminal" "$MCP_SERVER"
MCP_VERSION=$(json_get "$BODY" result.protocolVersion)
check_eq "MCP protocol version" "2024-11-05" "$MCP_VERSION"
MCP_LIST='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
req POST /mcp "$TOKEN" "$MCP_LIST"
check_status "POST /mcp tools/list" 200 "$STATUS"
MCP_TOOLS=$(json_len "$BODY" result.tools)
check_eq "MCP tools count matches the agent catalog" "$TOOLS_COUNT" "$MCP_TOOLS"

echo "== SSE /api/events"
curl -sS -N -m 7 -H "X-Terminal-Token: $TOKEN" "$BASE/api/events" >"$SSE_FILE" 2>/dev/null
SSE_FRAMES=$(grep -c '^data: {' "$SSE_FILE" 2>/dev/null)
if [ -z "$SSE_FRAMES" ]; then
  SSE_FRAMES=0
fi
check_ge "SSE data frames" 1 "$SSE_FRAMES"
if grep -qE '"type":"(quote|portfolio|status|heartbeat|_sse)"' "$SSE_FILE" 2>/dev/null; then
  pass "SSE has a known event type"
else
  fail "SSE has a known event type"
fi

echo "== shutdown"
kill -TERM "$SERVER_PID" 2>/dev/null
for _ in $(seq 1 100); do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
wait "$SERVER_PID"
EXIT_CODE=$?
SERVER_PID=""
check_eq "SIGTERM exit code" "0" "$EXIT_CODE"
if grep -q 'завершение работы' "$LOG" 2>/dev/null; then
  pass "graceful shutdown logged"
else
  fail "graceful shutdown logged"
fi

echo
TOTAL=$((PASS + FAIL))
if [ "$FAIL" -eq 0 ]; then
  echo "SMOKE PASS: $PASS/$TOTAL checks passed"
  exit 0
fi
echo "SMOKE FAIL: $FAIL of $TOTAL checks failed ($PASS passed)"
if [ "$SMOKE_KEEP" = "1" ] && [ -f "$LOG" ]; then
  echo "backend log: $LOG"
fi
exit 1
