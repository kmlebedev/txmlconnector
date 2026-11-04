#!/bin/sh
# Run the TRANSAQ DLL under Wine with tools/reference-harness and print the
# results and callbacks (procedure RE-2 in tools/re/README.md).
#
# usage: tools/re/run_dll.sh WAIT 'COMMAND' [sleep:5s] ['COMMAND' ...]
#
# Environment:
#   WINE          wine binary (macOS: ".../Wine Devel.app/Contents/Resources/wine/bin/wine")
#   WINEPREFIX    Wine prefix (create once with: $WINE wineboot -i)
#   TXC_DLL       path to txmlconnector64-6.47.2.26.4.dll
#   RE_WORKDIR    working directory (default ./.re-run); DLL logs go to $RE_WORKDIR/logs
#   RE_TIMEOUT    seconds before the run is killed (default 150)
#   TC_LOGIN, TC_PASSWORD  masked in the printed output if set
#
# The harness is rebuilt for windows/amd64 on every run. Full callback XML is
# kept in $RE_WORKDIR/last-run.out (it contains account data on real servers:
# keep it out of git); stdout shows command results and callbacks cut to 300
# characters.
set -eu
REPO=$(cd "$(dirname "$0")/../.." && pwd)
# Defaults match the local macOS setup (Wine 11.21 under Rosetta, prefix, bundled DLL);
# override WINE / WINEPREFIX / TXC_DLL to use another environment.
: "${WINE:=$HOME/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine}"
: "${WINEPREFIX:=$HOME/re/wine-prefix}"
: "${TXC_DLL:=$REPO/txmlconnector64-6.47.2.26.4.dll}"
[ -x "$WINE" ] || { echo "run_dll: WINE is not executable: $WINE (set WINE)" >&2; exit 1; }
[ -d "$WINEPREFIX" ] || { echo "run_dll: WINEPREFIX not found: $WINEPREFIX (create with: WINE=... \"\$WINE\" wineboot -i)" >&2; exit 1; }
[ -f "$TXC_DLL" ] || { echo "run_dll: TXC_DLL not found: $TXC_DLL (set TXC_DLL)" >&2; exit 1; }
WORK=${RE_WORKDIR:-./.re-run}
mkdir -p "$WORK/logs"
WORK=$(cd "$WORK" && pwd)
(cd "$REPO" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o "$WORK/harness.exe" ./tools/reference-harness)
cp -f "$TXC_DLL" "$WORK/txmlconnector.dll"
wait=$1
shift
cd "$WORK"
export WINEPREFIX WINEDEBUG=-all TC_DLL_PATH=txmlconnector.dll TC_DLL_LOG_DIR=logs TC_DLL_LOG_LEVEL=3
perl -e 'alarm shift; exec @ARGV' "${RE_TIMEOUT:-150}" "$WINE" harness.exe -wait "$wait" "$@" \
	> last-run.out 2>/dev/null || true
mask() {
	if [ -n "${TC_PASSWORD:-}" ]; then sed "s/${TC_PASSWORD}/***/g"; else cat; fi |
		if [ -n "${TC_LOGIN:-}" ]; then sed "s/${TC_LOGIN}/LOGIN/g"; else cat; fi
}
grep -aE "CMD:|CALLBACK:|^new|^start" last-run.out | mask | cut -c1-300
