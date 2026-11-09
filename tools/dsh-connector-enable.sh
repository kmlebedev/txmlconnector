#!/usr/bin/env bash
# Mount the read-only TRANSAQ connector (dshtrading-connector-transaq) into a
# DSH profile, by default the one DeepSeek Harness Desktop boots
# (~/.dsh/profiles/desktop). Idempotent; backs up package.json and
# cordis.patch.yml next to themselves before every change.
#
#   tools/dsh-connector-enable.sh            # enable
#   tools/dsh-connector-enable.sh --remove   # unmount the connector row and link
#
# Environment (all optional):
#   DSH_HOME       DSH home (default ~/.dsh)
#   PROFILE        profile name (default desktop)
#   GATEWAY_URL    loopback URL of bin/terminal (default http://127.0.0.1:18095;
#                  18090 is taken by the AI-terminal plugin's own backend)
#   TOKEN_REF      env/credential name holding the gateway token
#                  (default TERMINAL_API_TOKEN)
#   DSH            dsh launcher (default: dsh on PATH, else /usr/local/bin/dsh)
#   SKIP_INSTALL=1 only edit the profile, do not run `dsh plugin ... install`
#   DSH_PLUGIN_ARGS extra pnpm args for the install, e.g. "--offline"
set -u
MODE=enable
case "${1:-}" in
  "") ;;
  --remove) MODE=remove ;;
  -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
  *) echo "usage: $0 [--remove]" >&2; exit 2 ;;
esac

REPO="$(cd "$(dirname "$0")/.." && pwd)"
CONNECTOR_DIR="$REPO/dshtrading-connector-transaq"
DSH_HOME="${DSH_HOME:-$HOME/.dsh}"
PROFILE="${PROFILE:-desktop}"
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1:18095}"
TOKEN_REF="${TOKEN_REF:-TERMINAL_API_TOKEN}"
DSH="${DSH:-$(command -v dsh || echo /usr/local/bin/dsh)}"
P="$DSH_HOME/profiles/$PROFILE"

if [ ! -f "$CONNECTOR_DIR/index.js" ]; then
  echo "error: connector not found: $CONNECTOR_DIR" >&2
  exit 1
fi
if [ ! -f "$P/package.json" ]; then
  echo "error: DSH profile not found: $P (start DSH once, or run: $DSH plugin --profile $PROFILE list)" >&2
  exit 1
fi

TS="$(date +%Y%m%d-%H%M%S)"
cp "$P/package.json" "$P/package.json.connector-$TS" || exit 1
[ -f "$P/cordis.patch.yml" ] && cp "$P/cordis.patch.yml" "$P/cordis.patch.yml.connector-$TS"

python3 - "$P" "$CONNECTOR_DIR" "$GATEWAY_URL" "$TOKEN_REF" "$MODE" <<'PY' || exit 1
import json, os, re, sys

profile, connector_dir, gateway_url, token_ref, mode = sys.argv[1:6]
PKG = "@local/dshtrading-connector-transaq"
ROW_ID = "dsh-trading-ru-connector-transaq"
HOST = "@dshtrading/base"

pkg_path = os.path.join(profile, "package.json")
d = json.load(open(pkg_path))
deps = d.setdefault("dependencies", {})
bundles = d.setdefault("dsh", {}).setdefault("profile", {}).setdefault("bundles", [])

patch_path = os.path.join(profile, "cordis.patch.yml")
patch = open(patch_path).read() if os.path.exists(patch_path) else ""
row_re = re.compile(
    r"\n?# dshtrading-connector-transaq: BEGIN\n.*?# dshtrading-connector-transaq: END\n",
    re.S,
)

if mode == "remove":
    deps.pop(PKG, None)
    patch = row_re.sub("\n", patch)
    print("removed:", PKG, "row", ROW_ID, "(the @dshtrading/base host is left installed)")
else:
    deps[PKG] = "link:" + connector_dir
    # The connector registers into dsh-trading's registries (provided by the
    # @dshtrading/router row of the @dshtrading/base bundle); without the host
    # it only adds the ru_* tools.
    deps.setdefault(HOST, "^0.6.3")
    if HOST not in bundles:
        bundles.append(HOST)
    if ROW_ID in patch and not row_re.search(patch):
        sys.exit("error: %s already has a hand-written %s row; edit it manually" % (patch_path, ROW_ID))
    row = (
        "# dshtrading-connector-transaq: BEGIN\n"
        "- insert:\n"
        "    - id: %s\n"
        "      name: '%s'\n"
        "      config:\n"
        "        enabled: true\n"
        "        gatewayUrl: '%s'\n"
        "        tokenRef: %s\n"
        "# dshtrading-connector-transaq: END\n"
    ) % (ROW_ID, PKG, gateway_url, token_ref)
    patch = row_re.sub("\n", patch)
    if patch and not patch.endswith("\n"):
        patch += "\n"
    patch += "\n" + row
    print("dependencies:", PKG, "->", deps[PKG], ";", HOST, "->", deps[HOST])
    print("bundles:", bundles)
    print("row:", ROW_ID, "gatewayUrl", gateway_url, "tokenRef", token_ref)

json.dump(d, open(pkg_path, "w"), indent=2)
open(pkg_path, "a").write("\n")
open(patch_path, "w").write(patch)
PY

echo "backup: $P/package.json.connector-$TS"
if [ "${SKIP_INSTALL:-0}" != "1" ]; then
  echo "sync pnpm dependencies ($DSH plugin --profile $PROFILE install ${DSH_PLUGIN_ARGS:-})..."
  # shellcheck disable=SC2086
  "$DSH" plugin --profile "$PROFILE" install ${DSH_PLUGIN_ARGS:-} || {
    echo "install failed; restore with: cp $P/package.json.connector-$TS $P/package.json" >&2
    exit 1
  }
fi
if [ "$MODE" = enable ]; then
  echo "next: start the gateway with the same $TOKEN_REF, make it visible to the app, restart DSH"
  echo "      (see dshtrading-connector-transaq/QUICKSTART.md)"
else
  echo "restart DSH: osascript -e 'quit app \"DeepSeek Harness\"'; open -a \"DeepSeek Harness\""
fi
