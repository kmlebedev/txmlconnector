#!/usr/bin/env bash
# Re-enable the @local/dsh-ai-terminal bundle (host half) in the desktop DSH
# profile after tools/dsh-plugin-disable.sh or a recovery reset. Keeps a backup.
# The plugin now lives in the separate dsh-trading-transaq repository; set
# DSH_AI_TERMINAL_DIR to point at another checkout.
set -u
P="$HOME/.dsh/profiles/desktop"
PLUGIN_DIR="${DSH_AI_TERMINAL_DIR:-$HOME/GolandProjects/dsh-trading-transaq/dsh-ai-terminal}"
TS="$(date +%Y%m%d-%H%M%S)"
cp "$P/package.json" "$P/package.json.enable-$TS"
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
if [ ! -d "$PLUGIN_DIR" ]; then
  echo "warning: plugin checkout not found: $PLUGIN_DIR (set DSH_AI_TERMINAL_DIR)" >&2
fi
echo "sync pnpm dependencies..."
/usr/local/bin/dsh plugin --profile desktop install || echo "pnpm install failed; inspect output above"
echo "restart DSH: open -a \"DeepSeek Harness\""
