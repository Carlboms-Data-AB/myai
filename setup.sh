#!/usr/bin/env bash
set -euo pipefail

APP=local-ai-mac-mini
LABEL=se.carlbomsdata.local-ai-mlx-serve
OLD_LABEL=se.carlbomsdata.mlx-serve
CFG_DIR="$HOME/.config/$APP"
CFG="$CFG_DIR/config"
OC_CFG="$CFG_DIR/opencode.json"
STATE="$HOME/.local/state/$APP"
SHARE="$HOME/.local/share/$APP"
BIN="$HOME/.local/bin"
CMD="$BIN/local-ai"
INSTALLED="$SHARE/setup.sh"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
MODEL_DIR="$HOME/.mlx-serve/models"
ZSHRC="$HOME/.zshrc"
BEGIN="# >>> local-ai-mac-mini >>>"
END="# <<< local-ai-mac-mini <<<"

MODEL=mlx-community/Qwen3.5-9B-6bit
CONTEXT=131072
OUTPUT=16384
PORT=11234
WEB=true
EGO=false
IDLE=0
HOST=127.0.0.1
SRC="${BASH_SOURCE[0]:-$0}"

have(){ command -v "$1" >/dev/null 2>&1; }
load_cfg(){
  [[ -r "$CFG" ]] || return 0
  while IFS='=' read -r k v; do
    case "$k" in MODEL) MODEL="$v";; CONTEXT) CONTEXT="$v";; OUTPUT) OUTPUT="$v";; PORT) PORT="$v";; WEB) WEB="$v";; EGO) EGO="$v";; IDLE) IDLE="$v";; esac
  done < "$CFG"
}
validate(){
  [[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo "ERROR: Apple Silicon macOS required" >&2; return 1; }
  case "$MODEL" in *[!A-Za-z0-9._/-]*|'') echo "ERROR: invalid model" >&2; return 1;; esac
  for n in "$CONTEXT" "$OUTPUT" "$PORT" "$IDLE"; do case "$n" in *[!0-9]*|'') echo "ERROR: invalid numeric setting" >&2; return 1;; esac; done
  [[ "$PORT" -ge 1024 && "$PORT" -le 65535 ]] || { echo "ERROR: invalid port" >&2; return 1; }
  [[ "$WEB" == true || "$WEB" == false ]] || return 1
  [[ "$EGO" == true || "$EGO" == false ]] || return 1
}
mkdirs(){ mkdir -p "$CFG_DIR" "$STATE" "$SHARE" "$BIN" "$HOME/Library/LaunchAgents"; }
save_cfg(){
  mkdirs
  cat > "$CFG" <<EOF2
MODEL=$MODEL
CONTEXT=$CONTEXT
OUTPUT=$OUTPUT
PORT=$PORT
WEB=$WEB
EGO=$EGO
IDLE=$IDLE
EOF2
  chmod 600 "$CFG"
}
write_oc(){
  local ws=deny wf=deny
  [[ "$WEB" == true ]] && ws=allow && wf=allow
  cat > "$OC_CFG" <<EOF2
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "mlx/$MODEL",
  "enabled_providers": ["mlx"],
  "provider": {
    "mlx": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "MLX Serve (local)",
      "options": { "baseURL": "http://$HOST:$PORT/v1" },
      "models": {
        "$MODEL": {
          "name": "$MODEL",
          "limit": { "context": $CONTEXT, "output": $OUTPUT }
        }
      }
    }
  },
  "permission": { "websearch": "$ws", "webfetch": "$wf" }
}
EOF2
  chmod 600 "$OC_CFG"
}
write_plist(){
  local mlx idle=''
  mlx="$(command -v mlx-serve)"
  [[ "$IDLE" -gt 0 ]] && idle="<string>--idle-evict-secs</string><string>$IDLE</string>"
  cat > "$PLIST" <<EOF2
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$LABEL</string>
<key>ProgramArguments</key><array>
<string>$mlx</string><string>--serve</string><string>--model-dir</string><string>$MODEL_DIR</string>
<string>--host</string><string>$HOST</string><string>--port</string><string>$PORT</string>$idle
</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>$STATE/mlx-serve.log</string>
<key>StandardErrorPath</key><string>$STATE/mlx-serve-error.log</string>
</dict></plist>
EOF2
  plutil -lint "$PLIST" >/dev/null
}
target(){ printf 'gui/%s/%s' "$(id -u)" "$LABEL"; }
loaded(){ launchctl print "$(target)" >/dev/null 2>&1; }
stop_old(){ launchctl bootout "gui/$(id -u)/$OLD_LABEL" >/dev/null 2>&1 || true; }
listener(){ lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -n1; }
restart(){
  loaded && launchctl bootout "$(target)" >/dev/null 2>&1 || true
  stop_old
  local p="$(listener)"
  if [[ -n "$p" ]]; then
    local c="$(ps -p "$p" -o comm= 2>/dev/null || true)"
    if [[ "$c" == *mlx-serve* && -t 0 ]]; then
      printf 'Stop existing mlx-serve on port %s? [Y/n] ' "$PORT"; read -r a || a=''; [[ "$a" =~ ^[Nn] ]] || kill "$p" 2>/dev/null || true; sleep 1
    fi
  fi
  [[ -z "$(listener)" ]] || { echo "ERROR: port $PORT is already in use" >&2; return 1; }
  launchctl bootstrap "gui/$(id -u)" "$PLIST"
  launchctl kickstart -k "$(target)" >/dev/null
}
api(){ printf 'http://%s:%s' "$HOST" "$PORT"; }
wait_api(){ for _ in {1..30}; do curl -fsS --max-time 2 "$(api)/v1/models" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }
remove_block(){
  [[ -f "$ZSHRC" ]] || return 0
  awk -v b="$BEGIN" -v e="$END" '$0==b{s=1;next}$0==e{s=0;next}!s{print}' "$ZSHRC" > "$ZSHRC.tmp" && mv "$ZSHRC.tmp" "$ZSHRC"
}
install_cmd(){
  mkdirs
  [[ "$SRC" == "$INSTALLED" ]] || cp "$SRC" "$INSTALLED"
  chmod 755 "$INSTALLED"
  cat > "$CMD" <<EOF2
#!/usr/bin/env bash
exec "$INSTALLED" "\$@"
EOF2
  chmod 755 "$CMD"
  case ":$PATH:" in *":$BIN:"*) :;; *)
    touch "$ZSHRC"; remove_block; printf '\n%s\nexport PATH="$HOME/.local/bin:$PATH"\n%s\n' "$BEGIN" "$END" >> "$ZSHRC"; export PATH="$BIN:$PATH";; esac
}
install_stack(){
  have brew || { echo "ERROR: install Homebrew first: https://brew.sh" >&2; return 1; }
  mkdirs; load_cfg; validate
  echo; echo "Installing local AI stack..."; echo
  if ! have mlx-serve; then
    brew tap ddalcu/mlx-serve https://github.com/ddalcu/mlx-serve
    if brew help trust >/dev/null 2>&1; then brew trust --formula ddalcu/mlx-serve/mlx-serve; fi
    brew install ddalcu/mlx-serve/mlx-serve
  else echo "  ✓ mlx-serve installed"; fi
  if ! have opencode; then brew install anomalyco/tap/opencode; else echo "  ✓ OpenCode installed"; fi
  mlx-serve list 2>/dev/null | grep -Fq "$MODEL" || mlx-serve pull "$MODEL"
  save_cfg; write_oc; write_plist; install_cmd; restart
  wait_api && echo "  ✓ API ready at $(api)" || echo "  WARNING: API not ready yet"
  echo; echo "✓ Installed"; echo; echo "Run: local-ai"; echo
}
status(){
  load_cfg; echo; echo "Local AI status"; echo
  printf '  %-18s %s\n' mlx-serve "$(have mlx-serve && mlx-serve --version 2>/dev/null | head -n1 || echo not-installed)"
  printf '  %-18s %s\n' service "$(loaded && echo running || echo stopped)"
  printf '  %-18s %s\n' API "$(curl -fsS --max-time 2 "$(api)/v1/models" >/dev/null 2>&1 && echo "$(api)" || echo unreachable)"
  printf '  %-18s %s\n' model "$MODEL"
  printf '  %-18s %s\n' OpenCode "$(have opencode && opencode --version 2>/dev/null | head -n1 || echo not-installed)"
  printf '  %-18s %s\n' web-tools "$WEB"
  printf '  %-18s %s\n' ego-browser "$(have ego-browser && echo ready || echo not-ready)"
  echo
}
oc_env(){ export OPENCODE_CONFIG="$OC_CFG" OPENCODE_CONFIG_CONTENT="$(cat "$OC_CFG")"; [[ "$WEB" == true ]] && export OPENCODE_ENABLE_EXA=1 || unset OPENCODE_ENABLE_EXA 2>/dev/null || true; }
launch_oc(){ load_cfg; [[ -r "$OC_CFG" ]] || { echo "Run Install / update first" >&2; return 1; }; oc_env; opencode "$@"; }
test_stack(){
  load_cfg; local fail=0; echo; echo "Testing local AI stack..."; echo
  have mlx-serve && echo "  ✓ mlx-serve" || { echo "  ✗ mlx-serve"; fail=1; }
  loaded && echo "  ✓ launchd service" || { echo "  ✗ launchd service"; fail=1; }
  curl -fsS --max-time 3 "$(api)/v1/models" >/dev/null 2>&1 && echo "  ✓ API" || { echo "  ✗ API"; fail=1; }
  have opencode && echo "  ✓ OpenCode" || { echo "  ✗ OpenCode"; fail=1; }
  [[ -r "$OC_CFG" ]] && echo "  ✓ OpenCode config" || { echo "  ✗ OpenCode config"; fail=1; }
  if [[ "$fail" -eq 0 ]]; then
    local r; r="$(curl -fsS --max-time 180 -H 'Content-Type: application/json' -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply exactly: local-ai-ok\"}],\"max_tokens\":16,\"temperature\":0}" "$(api)/v1/chat/completions" 2>/dev/null || true)"
    printf '%s' "$r" | grep -Fq local-ai-ok && echo "  ✓ model inference" || { echo "  ✗ model inference"; fail=1; }
  fi
  [[ "$WEB" == true ]] && echo "  ✓ websearch/webfetch configured" || echo "  - web tools disabled"
  echo; [[ "$fail" -eq 0 ]] && echo "✓ Everything looks good." || echo "Some checks failed."; echo
  return "$fail"
}
prompt_num(){ local label="$1" cur="$2" min="$3" max="$4" v; printf '%s [%s]: ' "$label" "$cur"; read -r v || v=''; v="${v:-$cur}"; case "$v" in *[!0-9]*|'') return 1;; esac; [[ "$v" -ge "$min" && "$v" -le "$max" ]] || return 1; printf '%s' "$v"; }
apply(){ validate; save_cfg; write_oc; write_plist; restart; wait_api || true; }
configure(){
  load_cfg
  while true; do clear; echo "LOCAL AI · Configure"; echo; echo " 1  Model               $MODEL"; echo " 2  Context             $CONTEXT"; echo " 3  Output tokens       $OUTPUT"; echo " 4  Web tools           $WEB"; echo " 5  Browser automation  $EGO"; echo " 6  API port            $PORT"; echo " 7  Idle eviction       $IDLE"; echo " 8  Back"; echo; printf 'choose ❯ '; read -r c || c=8
    case "$c" in
      1) mlx-serve list || true; printf 'Model [%s]: ' "$MODEL"; read -r v || v=''; [[ -n "$v" ]] && MODEL="$v"; mlx-serve list 2>/dev/null | grep -Fq "$MODEL" || mlx-serve pull "$MODEL"; apply;;
      2) v="$(prompt_num Context "$CONTEXT" 4096 1048576)" && CONTEXT="$v" && apply || true;;
      3) v="$(prompt_num 'Output tokens' "$OUTPUT" 256 262144)" && OUTPUT="$v" && apply || true;;
      4) [[ "$WEB" == true ]] && WEB=false || WEB=true; apply;;
      5) if [[ "$EGO" == true ]]; then EGO=false; apply; else have npx || { printf 'Install Node.js? [y/N] '; read -r a || a=''; [[ "$a" =~ ^[Yy] ]] && brew install node || continue; }; npx skills add citrolabs/ego-lite; EGO=true; apply; fi;;
      6) v="$(prompt_num 'API port' "$PORT" 1024 65535)" && PORT="$v" && apply || true;;
      7) v="$(prompt_num 'Idle seconds' "$IDLE" 0 86400)" && IDLE="$v" && apply || true;;
      8) return;;
    esac
    printf '\n[enter] '; read -r _ || true
  done
}
uninstall_stack(){
  printf 'Remove local-ai managed files? [y/N] '; read -r a || a=''; [[ "$a" =~ ^[Yy] ]] || return 0
  loaded && launchctl bootout "$(target)" >/dev/null 2>&1 || true
  rm -f "$PLIST" "$CMD"; remove_block; rm -rf "$CFG_DIR" "$STATE"; rm -f "$INSTALLED"; rmdir "$SHARE" 2>/dev/null || true
  echo "Removed local-ai configuration. Models and packages were kept."
}
menu(){
  while true; do clear; echo "LOCAL AI · MAC MINI"; echo "OpenCode + mlx-serve + MLX"; echo; echo " 1  Launch OpenCode"; echo " 2  Install / update"; echo " 3  Configure"; echo " 4  Status"; echo " 5  Test"; echo " 6  Restart mlx-serve"; echo " 7  Uninstall"; echo " 8  Quit"; echo; printf 'choose ❯ '; read -r c || c=8
    case "$c" in 1) launch_oc;; 2) install_stack; read -r _ || true;; 3) configure;; 4) status; read -r _ || true;; 5) test_stack || true; read -r _ || true;; 6) restart; read -r _ || true;; 7) uninstall_stack; return;; 8) return;; esac
  done
}
main(){
  load_cfg
  case "${1:-}" in
    opencode|launch) shift; launch_oc "$@";;
    status) status;;
    test) test_stack;;
    restart) restart;;
    help|-h|--help) echo "local-ai [opencode|status|test|restart]";;
    '') [[ -t 0 && -t 1 ]] && menu || install_stack;;
    *) echo "local-ai [opencode|status|test|restart]" >&2; return 2;;
  esac
}
main "$@"
