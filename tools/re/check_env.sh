#!/usr/bin/env bash
# Environment readiness check for the native TRANSAQ client work
# (plan: docs/superpowers/plans/2026-11-04-native-transaq-client.md).
#
# Prints one PASS/WARN/FAIL/INFO line per check plus a final verdict.
# Never prints secret values (TC_LOGIN/TC_PASSWORD are reported as set/unset
# only), installs nothing, deletes nothing outside Go caches, and performs no
# network calls: GOPROXY=off and GOTOOLCHAIN=local are forced for this process.
#
# usage:
#   tools/re/check_env.sh          # quick checks (~1 s)
#   tools/re/check_env.sh --full   # + build, vet, test, -race, CGO-free crosses (~10-20 s)
#
# exit code: 0 when the environment is usable (no FAIL), 1 otherwise.

set -u

# Keep the check offline and pinned to the local toolchain: the script must not
# download modules or a Go toolchain behind the agent's back.
export GOPROXY=off
export GOTOOLCHAIN=local

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT" || { echo "FAIL: cannot cd to $REPO_ROOT"; exit 1; }

FULL=0
for arg in "$@"; do
	case "$arg" in
	--full) FULL=1 ;;
	-h | --help)
		sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "FAIL: unknown argument: $arg (use --full or --help)"
		echo "ИТОГО: PASS=0 WARN=0 FAIL=1 — окружение НЕ пригодно: неверный аргумент"
		exit 1
		;;
	esac
done

if [ -t 1 ]; then
	C_OK=$'\033[32m' C_WARN=$'\033[33m' C_FAIL=$'\033[31m' C_RST=$'\033[0m'
else
	C_OK= C_WARN= C_FAIL= C_RST=
fi

PASS=0 WARN=0 FAIL=0
pass() { PASS=$((PASS + 1)); printf '%sPASS%s %s\n' "$C_OK" "$C_RST" "$*"; }
warn() { WARN=$((WARN + 1)); printf '%sWARN%s %s\n' "$C_WARN" "$C_RST" "$*"; }
fail() { FAIL=$((FAIL + 1)); printf '%sFAIL%s %s\n' "$C_FAIL" "$C_RST" "$*"; }
info() { printf 'INFO %s\n' "$*"; }

# Temporary output files are removed on exit.
TMP_OUT="$(mktemp "${TMPDIR:-/tmp}/check_env.XXXXXX")" || exit 1
trap 'rm -f "$TMP_OUT"' EXIT

# Returns 0 when the directory exists and is writable. With create=1 the
# directory is created first (only used for the repo-local Go cache; never for
# paths outside the workspace). The probe runs in a subshell so a denied
# redirect does not leak to stderr.
writable_dir() {
	local dir="$1" create="${2:-0}"
	if [ "$create" = 1 ]; then
		mkdir -p "$dir" 2>/dev/null || return 1
	fi
	[ -d "$dir" ] && [ -w "$dir" ] || return 1
	( : >"$dir/.check_env_probe" ) 2>/dev/null || return 1
	rm -f "$dir/.check_env_probe" 2>/dev/null
	return 0
}

# The DSH file sandbox (workspace-write) denies writes outside the workspace:
# the global Go build cache (~/Library/Caches/go-build) and the module stat
# cache are not writable, so go build/vet/test fail with "operation not
# permitted". Fall back to a repository-local cache, exactly like `make verify`.
if command -v go >/dev/null 2>&1; then
	GO_CACHE="$(go env GOCACHE 2>/dev/null || true)"
	if [ -n "$GO_CACHE" ] && writable_dir "$GO_CACHE"; then
		info "GOCACHE по умолчанию доступен на запись: $GO_CACHE"
	else
		export GOCACHE="$REPO_ROOT/.cache/go-build"
		if writable_dir "$GOCACHE" 1; then
			warn "глобальный кэш Go недоступен на запись (песочница DSH workspace-write, ожидаемо); использую repo-local GOCACHE=$GOCACHE (так же делает make verify)"
		else
			fail "нет writable GOCACHE: go build/vet/test не смогут работать"
		fi
	fi
fi

# ---- Go toolchain -----------------------------------------------------------
if command -v go >/dev/null 2>&1; then
	GO_VERSION="$(go version 2>/dev/null)"
	case "$GO_VERSION" in
	*go1.27.*darwin/arm64*) pass "go: $GO_VERSION (ожидаемая версия проекта)" ;;
	*go1.27.*) warn "go: $GO_VERSION (ожидается darwin/arm64; кросс-сборки проверяются отдельно)" ;;
	*) warn "go: $GO_VERSION (проект ожидает go1.27.x)" ;;
	esac
	CGO_HOST="$(go env CGO_ENABLED 2>/dev/null)"
	info "CGO_ENABLED на хосте=$CGO_HOST; native/... должны собираться с CGO_ENABLED=0"
else
	fail "go: не найден в PATH"
fi

if command -v gofmt >/dev/null 2>&1; then
	# Only tracked Go files: .re-run/ holds gitignored RE scratch programs whose
	# formatting is not the repository's concern (same scope as make verify).
	UNFORMATTED="$(git ls-files '*.go' 2>/dev/null | xargs gofmt -l 2>/dev/null | head -5)"
	if [ -z "$UNFORMATTED" ]; then
		pass "gofmt tracked Go files: пусто"
	else
		fail "gofmt tracked Go files: требуется форматирование: $(echo "$UNFORMATTED" | tr '\n' ' ')"
	fi
else
	fail "gofmt: не найден в PATH"
fi

if command -v go >/dev/null 2>&1; then
	if go vet ./... >"$TMP_OUT" 2>&1; then
		pass "go vet ./...: без диагностик"
	else
		fail "go vet ./...: $(tail -3 "$TMP_OUT" | tr '\n' ' ')"
	fi
fi

if [ "$FULL" -eq 1 ]; then
	if go build ./... >"$TMP_OUT" 2>&1; then
		pass "go build ./...: ok"
	else
		fail "go build ./...: $(tail -3 "$TMP_OUT" | tr '\n' ' ')"
	fi

	START=$SECONDS
	if go test ./... -count=1 >"$TMP_OUT" 2>&1; then
		pass "go test ./... -count=1: ok ($((SECONDS - START))s)"
	else
		fail "go test ./... -count=1: $(grep -E '^(FAIL|--- FAIL)' "$TMP_OUT" | head -5 | tr '\n' ' ')"
	fi

	START=$SECONDS
	if go test -race ./... -count=1 >"$TMP_OUT" 2>&1; then
		pass "go test -race ./... -count=1: ok ($((SECONDS - START))s)"
	else
		fail "go test -race ./...: $(grep -E '^(FAIL|--- FAIL)' "$TMP_OUT" | head -5 | tr '\n' ' ')"
	fi

	for target in "linux amd64" "darwin arm64" "linux arm64"; do
		set -- $target
		if CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build ./native/... >"$TMP_OUT" 2>&1; then
			pass "кросс-сборка CGO_ENABLED=0 GOOS=$1 GOARCH=$2 go build ./native/...: ok"
		else
			fail "кросс-сборка $1/$2: $(tail -3 "$TMP_OUT" | tr '\n' ' ')"
		fi
	done
else
	info "build/test/cross-сборки пропущены (запустите с --full)"
fi

# ---- Ghidra / JDK (RE-1) ----------------------------------------------------
GHIDRA_HEADLESS="${GHIDRA_HEADLESS:-/opt/homebrew/opt/ghidra/libexec/support/analyzeHeadless}"
if [ -x "$GHIDRA_HEADLESS" ]; then
	GHIDRA_PROPS="$(dirname "$GHIDRA_HEADLESS")/../Ghidra/application.properties"
	GHIDRA_VERSION="$(grep -m1 '^application.version=' "$GHIDRA_PROPS" 2>/dev/null | cut -d= -f2)"
	pass "Ghidra analyzeHeadless: $GHIDRA_HEADLESS (${GHIDRA_VERSION:-версия неизвестна})"
else
	fail "Ghidra analyzeHeadless не найден: $GHIDRA_HEADLESS"
fi

JAVA_CANDIDATE=""
if [ -n "${JAVA_HOME:-}" ] && [ -x "$JAVA_HOME/bin/java" ] && "$JAVA_HOME/bin/java" -version >/dev/null 2>&1; then
	JAVA_CANDIDATE="$JAVA_HOME/bin/java"
elif command -v java >/dev/null 2>&1 && java -version >/dev/null 2>&1; then
	JAVA_CANDIDATE="java"
else
	for java_home in \
		/opt/homebrew/opt/openjdk@25/libexec/openjdk.jdk/Contents/Home \
		/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home; do
		if [ -x "$java_home/bin/java" ] && "$java_home/bin/java" -version >/dev/null 2>&1; then
			JAVA_CANDIDATE="$java_home/bin/java"
			JAVA_HOME="$java_home"
			break
		fi
	done
fi

if [ -n "$JAVA_CANDIDATE" ]; then
	JAVA_VERSION="$("$JAVA_CANDIDATE" -version 2>&1 | head -1)"
	case "$JAVA_VERSION" in
	*'"25.'*) pass "JVM: $JAVA_VERSION (совместим с Ghidra 12.2)" ;;
	*'"17.'*) warn "JVM: $JAVA_VERSION — Ghidra 12.2 (Homebrew) рассчитана на openjdk@25; задайте JAVA_HOME" ;;
	*) warn "JVM: $JAVA_VERSION — ожидается JDK 25 (openjdk@25) для Ghidra 12.2" ;;
	esac
	if [ -z "${JAVA_HOME:-}" ]; then
		warn "JAVA_HOME не задан: export JAVA_HOME=/opt/homebrew/opt/openjdk@25/libexec/openjdk.jdk/Contents/Home"
	fi
else
	fail "JVM не найдена (java в PATH не работает, JAVA_HOME не задан): RE-1 недоступен"
fi

GHIDRA_SETTINGS="$HOME/Library/ghidra"
if writable_dir "$GHIDRA_SETTINGS"; then
	pass "каталог настроек Ghidra доступен на запись: $GHIDRA_SETTINGS"
else
	warn "каталог $GHIDRA_SETTINGS недоступен на запись (sandbox): analyzeHeadless упадёт на LaunchSupport -save; обход: mkdir -p \"\${TMPDIR:-/tmp}/dsh-ghidra-home\" && export JAVA_TOOL_OPTIONS=-Duser.home=\${TMPDIR:-/tmp}/dsh-ghidra-home"
fi

GHIDRA_SCRIPTS_MISSING=""
for script in CallArgs Callers ScalarRefs StrRefs XrefDecomp; do
	[ -f "tools/re/ghidra/$script.java" ] || GHIDRA_SCRIPTS_MISSING="$GHIDRA_SCRIPTS_MISSING $script"
done
if [ -z "$GHIDRA_SCRIPTS_MISSING" ]; then
	pass "Ghidra-скрипты tools/re/ghidra: 5/5 на месте"
else
	fail "нет Ghidra-скриптов:$GHIDRA_SCRIPTS_MISSING"
fi

if arch -x86_64 /usr/bin/true >/dev/null 2>&1; then
	pass "Rosetta (x86_64) доступна"
else
	warn "Rosetta недоступна: macOS-Wine (x86_64) не запустится"
fi

# ---- Python RE tooling ------------------------------------------------------
if command -v python3 >/dev/null 2>&1; then
	PY_VERSION="$(python3 --version 2>&1)"
	pass "python3: $PY_VERSION"
	PYTHONDONTWRITEBYTECODE=1 python3 tools/re/py/selftest.py >"$TMP_OUT" 2>&1
	if grep -q 'txproto selftest: OK' "$TMP_OUT"; then
		pass "tools/re/py/selftest.py: txproto selftest: OK"
	else
		fail "tools/re/py/selftest.py: нет 'txproto selftest: OK' ($(tail -2 "$TMP_OUT" | tr '\n' ' '))"
	fi
else
	fail "python3: не найден в PATH"
fi

# ---- Wine (RE-2/RE-3) -------------------------------------------------------
WINE_BIN="${WINE:-}"
if [ -z "$WINE_BIN" ]; then
	WINE_BIN="$(command -v wine 2>/dev/null || command -v wine64 2>/dev/null || true)"
fi
# Standard local macOS install (Wine 11.21 under Rosetta), see docs/deepseek/environment.md.
if [ -z "$WINE_BIN" ] && [ -x "$HOME/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine" ]; then
	WINE_BIN="$HOME/re/wine/Wine Devel.app/Contents/Resources/wine/bin/wine"
fi
WINE_PREFIX_CHECK="${WINEPREFIX:-$HOME/re/wine-prefix}"
if [ -n "$WINE_BIN" ] && [ -x "$WINE_BIN" ]; then
	WINE_VERSION="$("$WINE_BIN" --version 2>/dev/null | tail -1)"
	if [ -d "$WINE_PREFIX_CHECK" ]; then
		pass "Wine: ${WINE_VERSION:-unknown} ($WINE_BIN), WINEPREFIX=$WINE_PREFIX_CHECK — RE-2/RE-3 доступны (нужны TC_PASSWORD и/или записанная сессия; см. tools/re/README.md)"
	else
		warn "Wine есть (${WINE_VERSION:-unknown}), но WINEPREFIX не найден ($WINE_PREFIX_CHECK): RE-2/RE-3 пока недоступны (создать: WINE=... \"\$WINE\" wineboot -i)"
	fi
else
	WINE_HINT=""
	[ -d "$HOME/re/wine" ] && WINE_HINT=" (каталог ~/re/wine есть, но wine не найден)"
	warn "Wine не найден$WINE_HINT: RE-2/RE-3 (живой DLL, оракул) недоступны до установки Wine — см. tools/re/README.md §Окружение"
fi

# ---- Demo server credentials (presence only, values are never printed) ------
if [ -n "${TC_LOGIN:-}" ]; then
	pass "TC_LOGIN: set (значение не печатается)"
else
	warn "TC_LOGIN: unset — живой логин на демо-сервере недоступен"
fi
if [ -n "${TC_PASSWORD:-}" ]; then
	pass "TC_PASSWORD: set (значение не печатается)"
else
	warn "TC_PASSWORD: unset — живой логин и расшифровка захватов (wire-decode) недоступны"
fi
if [ -n "${TC_DLL_PATH:-}" ]; then
	pass "TC_DLL_PATH: set"
elif [ -n "${TXC_DLL:-}" ]; then
	pass "TXC_DLL: set"
elif [ -f "$REPO_ROOT/txmlconnector64-6.47.2.26.4.dll" ]; then
	info "TC_DLL_PATH/TXC_DLL: unset, но run_dll.sh подставит DLL из корня репозитория"
else
	warn "TC_DLL_PATH/TXC_DLL: unset — tools/re/run_dll.sh требует TXC_DLL (DLL лежит в корне репозитория)"
fi

# Secrets file: presence only; the script never reads or prints its values.
if [ -f "$HOME/re/demo.env" ]; then
	info "секреты: $HOME/re/demo.env найден (не читается и не печатается; загрузить: set -a; . ~/re/demo.env; set +a)"
else
	info "секреты: ~/re/demo.env не найден — шаблон tools/re/demo.env.example; значения TC_LOGIN/TC_PASSWORD только в этом файле"
fi

# ---- Reference DLL ----------------------------------------------------------
DLL="txmlconnector64-6.47.2.26.4.dll"
DLL_REF_SHA="a197e2625e53c0df453687ae1b20ef2ae1a05e034cd20c424a7014b0c9726342"
if [ -f "$DLL" ]; then
	if command -v shasum >/dev/null 2>&1; then
		DLL_SHA="$(shasum -a 256 "$DLL" | awk '{print $1}')"
	else
		DLL_SHA="$(sha256sum "$DLL" | awk '{print $1}')"
	fi
	if [ "$DLL_SHA" = "$DLL_REF_SHA" ]; then
		pass "эталонная DLL: $DLL, sha256 совпадает"
	else
		fail "эталонная DLL: $DLL, sha256 НЕ совпадает ($DLL_SHA)"
	fi
else
	fail "эталонная DLL отсутствует: $DLL"
fi

# ---- Start state of the plan (informational) --------------------------------
MISSING_TOOLS=""
for path in tools/tcproxy tools/replay-server native/replay native/model native/records; do
	[ -e "$path" ] || MISSING_TOOLS="$MISSING_TOOLS $path"
done
if [ -n "$MISSING_TOOLS" ]; then
	info "стартовое состояние: пока нет$MISSING_TOOLS (ожидаемо до Фазы 0 плана)"
else
	info "все ожидаемые инструменты плана уже присутствуют"
fi
info "фикстуры: native/records/testdata ($(find native/records/testdata -type f 2>/dev/null | wc -l | tr -d ' ') файлов)"

# ---- Git --------------------------------------------------------------------
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	GIT_BRANCH="$(git branch --show-current 2>/dev/null)"
	MODIFIED="$(git status --porcelain 2>/dev/null | awk '$1 ~ /^M/ {print $2}' | head -5 | tr '\n' ' ')"
	if [ -z "$MODIFIED" ]; then
		pass "git: ветка ${GIT_BRANCH:-?}, правок отслеживаемых файлов нет"
	else
		warn "git: ветка ${GIT_BRANCH:-?}, незакоммиченные правки: $MODIFIED (убедитесь, что это не ваша задача)"
	fi
	UNTRACKED="$(git status --porcelain 2>/dev/null | awk '$1 == "??" {print $2}' | head -5 | tr '\n' ' ')"
	[ -n "$UNTRACKED" ] && info "git: неотслеживаемые файлы: $UNTRACKED"
else
	warn "git: $REPO_ROOT не является рабочим деревом git"
fi

# ---- Verdict ----------------------------------------------------------------
echo
if [ "$FAIL" -eq 0 ]; then
	printf '%sИТОГО: PASS=%d WARN=%d FAIL=%d — окружение пригодно для работы%s\n' "$C_OK" "$PASS" "$WARN" "$FAIL" "$C_RST"
	exit 0
fi
printf '%sИТОГО: PASS=%d WARN=%d FAIL=%d — окружение НЕ пригодно: устраните FAIL (см. docs/deepseek/environment.md)%s\n' "$C_FAIL" "$PASS" "$WARN" "$FAIL" "$C_RST"
exit 1
