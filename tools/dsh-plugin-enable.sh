#!/usr/bin/env bash
# Re-enable the @local/dsh-ai-terminal bundle (host half) in the desktop DSH
# profile after tools/dsh-plugin-disable.sh or a recovery reset. Keeps a backup.
# The plugin now lives in the separate dsh-trading-transaq repository; set
# DSH_AI_TERMINAL_DIR to point at another checkout. Before the split branch is
# merged the plugin still exists in this checkout, which is used as a fallback.
set -u
P="$HOME/.dsh/profiles/desktop"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
NEW_DIR="$HOME/GolandProjects/dsh-trading-transaq/dsh-ai-terminal"
LEGACY_DIR="$REPO/dsh-ai-terminal"
if [ -n "${DSH_AI_TERMINAL_DIR:-}" ]; then
  PLUGIN_DIR="$DSH_AI_TERMINAL_DIR"
elif [ -f "$NEW_DIR/index.js" ]; then
  PLUGIN_DIR="$NEW_DIR"
elif [ -f "$LEGACY_DIR/index.js" ]; then
  PLUGIN_DIR="$LEGACY_DIR"
else
  echo "error: dsh-ai-terminal checkout not found: $NEW_DIR" >&2
  echo "       clone https://github.com/kmlebedev/dsh-trading-transaq or set DSH_AI_TERMINAL_DIR" >&2
  exit 1
fi
if [ ! -f "$PLUGIN_DIR/index.js" ]; then
  echo "error: not a dsh-ai-terminal checkout (index.js is missing): $PLUGIN_DIR" >&2
  exit 1
fi
TS="$(date +%Y%m%d-%H%M%S)"
cp "$P/package.json" "$P/package.json.enable-$TS" || exit 1
python3 - "$P" "$PLUGIN_DIR" <<'PY'
import json, sys
p = sys.argv[1] + "/package.json"
plugin_dir = sys.argv[2]
d = json.load(open(p))
d.setdefault("dsh", {}).setdefault("profile", {}).setdefault("bundles", [])
if "@local/dsh-ai-terminal" not in d["dsh"]["profile"]["bundles"]:
    d["dsh"]["profile"]["bundles"].append("@local/dsh-ai-terminal")
d.setdefault("dependencies", {})["@local/dsh-ai-terminal"] = "link:" + plugin_dir
json.dump(d, open(p, "w"), indent=2)
open(p, "a").write("\n")
print("bundles:", d["dsh"]["profile"]["bundles"])
print("link:", plugin_dir)
PY
echo "backup: $P/package.json.enable-$TS"
echo "sync pnpm dependencies..."
/usr/local/bin/dsh plugin --profile desktop install || echo "pnpm install failed; inspect output above"
echo "restart DSH: open -a \"DeepSeek Harness\""
