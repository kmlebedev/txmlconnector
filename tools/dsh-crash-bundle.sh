#!/usr/bin/env bash
# Collect the latest DSH crash report plus the boot state (profile, plugin link,
# git HEAD, processes, ports) into one tarball for the agent to inspect.
#
# Safety: only logs and profile config are copied. ~/.dsh/.credentials.yaml,
# ~/re/demo.env and session databases are NOT included.
set -u
REPO="$(cd "$(dirname "$0")/.." && pwd)"
LOGS="$HOME/Library/Logs/DeepSeek Harness"
PROFILE="$HOME/.dsh/profiles/desktop"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$HOME/dsh-crash-$STAMP"
mkdir -p "$OUT/logs" "$OUT/profile" "$OUT/repo"

echo "== DSH crash bundle: $OUT"
if [ -d "$LOGS" ]; then
  ls -lt "$LOGS" > "$OUT/logs/ls.txt" 2>&1 || true
  ls -t "$LOGS"/crash-*.log 2>/dev/null | head -5 | while IFS= read -r f; do cp "$f" "$OUT/logs/"; done
fi
NEWEST="$(ls -t "$LOGS"/crash-*.log 2>/dev/null | head -1 || true)"
if [ -n "$NEWEST" ]; then
  cp "$NEWEST" "$OUT/newest-crash.log" 2>/dev/null || true
  cp "$NEWEST" "$OUT/logs/" 2>/dev/null || true
  { echo "newest: $NEWEST"; sed -n "1,80p" "$NEWEST"; } > "$OUT/newest-crash-head.txt" 2>&1 || true
  echo "-- newest crash log: $NEWEST"
fi

for f in package.json cordis.patch.yml cordis.yml pnpm-lock.yaml; do
  if [ -f "$PROFILE/$f" ]; then cp "$PROFILE/$f" "$OUT/profile/"; fi
done
readlink "$PROFILE/node_modules/@local/dsh-ai-terminal" > "$OUT/profile/plugin-link.txt" 2>&1 || true
PLUGIN_DIR="${DSH_AI_TERMINAL_DIR:-$HOME/GolandProjects/dsh-trading-transaq/dsh-ai-terminal}"
if [ ! -f "$PLUGIN_DIR/index.js" ] && [ -f "$REPO/dsh-ai-terminal/index.js" ]; then
  PLUGIN_DIR="$REPO/dsh-ai-terminal" # fallback: pre-split checkout
fi
if [ -f "$PLUGIN_DIR/index.js" ]; then
  printf '%s\n' "$PLUGIN_DIR" > "$OUT/profile/plugin-dir.txt" 2>&1 || true
  shasum -a 256 "$PLUGIN_DIR/index.js" > "$OUT/profile/plugin-index.sha256" 2>&1 || true
  grep -n "export const inject" "$PLUGIN_DIR/index.js" > "$OUT/profile/plugin-inject.txt" 2>&1 || true
fi
if [ -d "$REPO/.git" ]; then
  git -C "$REPO" status --short > "$OUT/repo/git-status.txt" 2>&1 || true
  git -C "$REPO" branch --show-current > "$OUT/repo/git-branch.txt" 2>&1 || true
  git -C "$REPO" log -n 5 --oneline > "$OUT/repo/git-log.txt" 2>&1 || true
  git -C "$REPO" rev-parse HEAD > "$OUT/repo/git-head.txt" 2>&1 || true
fi
ps aux | grep -E "DeepSeek Harness|bin/terminal" | grep -v grep > "$OUT/processes.txt" 2>&1 || true
lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | grep -E "DeepSeek|terminal" > "$OUT/ports.txt" 2>&1 || true

TARBALL="$OUT.tar.gz"
tar -czf "$TARBALL" -C "$HOME" "$(basename "$OUT")"
printf '%s\n' "$TARBALL" > "$HOME/dsh-crash-latest.txt"
echo "== tarball: $TARBALL"
echo "== path saved to: $HOME/dsh-crash-latest.txt"
echo "== send this path to the agent (paste it into the DSH chat):"
echo "$TARBALL"