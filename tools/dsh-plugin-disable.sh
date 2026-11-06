#!/usr/bin/env bash
# Emergency: remove the @local/dsh-ai-terminal bundle from the desktop DSH profile.
# Use it from Terminal when Desktop offers the fatal-recovery dialog and the chat
# is unavailable. Backs the profile up next to itself and keeps dependencies
# untouched, so the plugin can be re-enabled later.
set -u
P="$HOME/.dsh/profiles/desktop"
TS="$(date +%Y%m%d-%H%M%S)"
cp "$P/package.json" "$P/package.json.disable-$TS"
python3 - "$P" <<'PY'
import json, sys
p = sys.argv[1] + "/package.json"
d = json.load(open(p))
bundles = d.get("dsh", {}).get("profile", {}).get("bundles", [])
kept = [b for b in bundles if b != "@local/dsh-ai-terminal"]
d["dsh"]["profile"]["bundles"] = kept
json.dump(d, open(p, "w"), indent=2)
open(p, "a").write("\n")
print("bundles now:", kept)
PY
echo "backup: $P/package.json.disable-$TS"
echo "restart DSH: open -a \"DeepSeek Harness\""