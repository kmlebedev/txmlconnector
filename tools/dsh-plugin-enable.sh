#!/usr/bin/env bash
# Re-enable the @local/dsh-ai-terminal bundle (host half) in the desktop DSH
# profile after tools/dsh-plugin-disable.sh or a recovery reset. Keeps a backup.
set -u
P="$HOME/.dsh/profiles/desktop"
TS="$(date +%Y%m%d-%H%M%S)"
cp "$P/package.json" "$P/package.json.enable-$TS"
python3 - "$P" <<'PY'
import json, sys
p = sys.argv[1] + "/package.json"
d = json.load(open(p))
d.setdefault("dsh", {}).setdefault("profile", {}).setdefault("bundles", [])
if "@local/dsh-ai-terminal" not in d["dsh"]["profile"]["bundles"]:
    d["dsh"]["profile"]["bundles"].append("@local/dsh-ai-terminal")
d.setdefault("dependencies", {})["@local/dsh-ai-terminal"] = 
    "link:/Users/whitefox/GolandProjects/txmlconnector/dsh-ai-terminal"
json.dump(d, open(p, "w"), indent=2)
open(p, "a").write("\n")
print("bundles:", d["dsh"]["profile"]["bundles"])
PY
echo "backup: $P/package.json.enable-$TS"
echo "sync pnpm dependencies..."
/usr/local/bin/dsh plugin --profile desktop install || echo "pnpm install failed; inspect output above"
echo "restart DSH: open -a \"DeepSeek Harness\""