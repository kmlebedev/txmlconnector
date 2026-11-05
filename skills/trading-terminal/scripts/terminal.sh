#!/bin/sh
# terminal.sh — read-only REST access to the AI terminal backend (plan Task 25).
# Installed by: npx skills add github.com/kmlebedev/txmlconnector --skill trading-terminal
#
# Environment (.env): TERMINAL_URL (default http://127.0.0.1:8090) and
# TERMINAL_API_TOKEN. The token and the TRANSAQ credentials live only in .env:
# never paste them into a chat, a prompt or a SKILL.md. The script is read-only:
# it never places, cancels or moves orders.

set -eu

usage() {
	cat <<EOF
usage: terminal.sh <command> [args]

commands:
  health                                GET /api/health
  instruments                           GET /api/instruments
  quote SYMBOL                          quote and order book of one instrument
  book SYMBOL                           order book snapshot
  trades SYMBOL [LIMIT]                 recent trades (limit 50)
  history SYMBOL [PERIOD_SEC] [COUNT]   candles (period 60/300/900/3600/86400)
  strategies                            backtest strategy catalog
  backtest SYMBOL STRATEGY [PERIOD_SEC] run one backtest
  news [LIMIT]                          recorded TRANSAQ news (md.news)
  vol SYMBOL                            volatility oracle forecast

Environment (from .env, never from a chat):
  TERMINAL_URL       backend URL, default http://127.0.0.1:8090
  TERMINAL_API_TOKEN local API token (X-Terminal-Token header)

The script is read-only: it never places, cancels or moves orders.
EOF
}

die() {
	echo "$1" >&2
	exit 1
}

load_env() {
	for file in "${ENV_FILE:-}" ./.env ../.env "${HOME:-}/.config/ai-terminal/.env"; do
		[ -n "$file" ] || continue
		[ -f "$file" ] || continue
		while IFS= read -r line || [ -n "$line" ]; do
			case "$line" in
				""|"#"*) continue ;;
			esac
			case "$line" in
				*=*) ;;
				*) continue ;;
			esac
			key=${line%%=*}
			value=${line#*=}
			key=$(printf '%s' "$key" | tr -d " ")
			case "$key" in
				TERMINAL_URL|TERMINAL_API_TOKEN|TC_LOGIN|TC_PASSWORD) ;;
				*) continue ;;
			esac
			export "$key=$value"
		done < "$file"
		return 0
	done
	return 0
}

request() {
	method=$1
	path=$2
	body=${3:-}
	base=${TERMINAL_URL:-http://127.0.0.1:8090}
	url=$base$path
	if [ -n "${TERMINAL_API_TOKEN:-}" ]; then
		if [ -n "$body" ]; then
			curl -fsS -X "$method" -H "Content-Type: application/json" -H "X-Terminal-Token: $TERMINAL_API_TOKEN" --data "$body" "$url" && echo
		else
			curl -fsS -X "$method" -H "X-Terminal-Token: $TERMINAL_API_TOKEN" "$url" && echo
		fi
	else
		if [ -n "$body" ]; then
			curl -fsS -X "$method" -H "Content-Type: application/json" --data "$body" "$url" && echo
		else
			curl -fsS -X "$method" "$url" && echo
		fi
	fi
}

symbol_arg() {
	[ $# -ge 1 ] || die "symbol is required, for example TQBR:SBER"
	symbol=$(printf '%s' "$1" | tr "[:lower:]" "[:upper:]")
	case "$symbol" in
		*:*) ;;
		*) symbol=TQBR:$symbol ;;
	esac
	case "$symbol" in
		*[!A-Za-z0-9:._-]*) die "bad symbol: $symbol" ;;
	esac
	printf '%s' "$symbol"
}

encoded_symbol() {
	symbol=$(symbol_arg "$1")
	printf '%s' "$symbol" | sed "s/:/%3A/g"
}

positive_int() {
	case "${1:-}" in
		""|*[!0-9]*) die "expected a positive integer, got: ${1:-}" ;;
	esac
}

load_env
[ $# -ge 1 ] || { usage; exit 2; }
if [ -z "${TERMINAL_API_TOKEN:-}" ]; then
	echo "terminal.sh: TERMINAL_API_TOKEN is not set (put it into .env); requests fail if the backend is guarded" >&2
fi
command=$1
shift
case "$command" in
	health) request GET /api/health ;;
	instruments) request GET /api/instruments ;;
	strategies) request GET /api/backtest/strategies ;;
	quote)
		symbol=$(symbol_arg "${1:-}")
		body=$(printf '{"args":{"symbol":"%s"}}' "$symbol")
		request POST /api/agent/tools/get_quote/invoke "$body"
		;;
	book)
		symbol=$(encoded_symbol "${1:-}")
		request GET "/api/instruments/$symbol/orderbook"
		;;
	trades)
		symbol=$(encoded_symbol "${1:-}")
		limit=${2:-50}
		positive_int "$limit"
		request GET "/api/instruments/$symbol/trades?limit=$limit"
		;;
	history)
		symbol=$(encoded_symbol "${1:-}")
		period=${2:-300}
		count=${3:-500}
		positive_int "$period"
		positive_int "$count"
		request GET "/api/instruments/$symbol/candles?period=$period&count=$count"
		;;
	backtest)
		symbol=$(symbol_arg "${1:-}")
		strategy=${2:-}
		[ -n "$strategy" ] || die "strategy is required: run 'terminal.sh strategies' for the catalog"
		period=${3:-300}
		positive_int "$period"
		body=$(printf '{"symbol":"%s","strategy":"%s","periodSec":%s}' "$symbol" "$strategy" "$period")
		request POST /api/backtest "$body"
		;;
	news)
		limit=${1:-20}
		positive_int "$limit"
		body=$(printf '{"args":{"limit":%s}}' "$limit")
		request POST /api/agent/tools/get_news/invoke "$body"
		;;
	vol)
		symbol=$(symbol_arg "${1:-}")
		body=$(printf '{"args":{"symbol":"%s"}}' "$symbol")
		request POST /api/agent/tools/vol_forecast/invoke "$body"
		;;
	*) usage; die "unknown command: $command" ;;
esac

