#!/usr/bin/env bash
set -euo pipefail

APP=local-ai-mac-mini
MLX_LABEL=se.carlbomsdata.local-ai-mlx-serve
WEB_LABEL=se.carlbomsdata.local-ai-opencode-web
OLD_LABEL=se.carlbomsdata.mlx-serve
CFG_DIR="$HOME/.config/$APP"
CFG="$CFG_DIR/config"
OC_CFG="$CFG_DIR/opencode.json"
AUTH="$CFG_DIR/web-auth"
STATE="$HOME/.local/state/$APP"
SHARE="$HOME/.local/share/$APP"
BIN="$HOME/.local/bin"
CMD="$BIN/local-ai"
INSTALLED="$SHARE/setup.sh"
WEB_RUNNER="$SHARE/opencode-web.sh"
MLX_PLIST="$HOME/Library/LaunchAgents/$MLX_LABEL.plist"
WEB_PLIST="$HOME/Library/LaunchAgents/$WEB_LABEL.plist"
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
WEB_HOST=0.0.0.0
WEB_PORT=4096
WEB_USERNAME=opencode
WEB_PASSWORD=
SRC="${BASH_SOURCE[0]:-$0}"

have(){ command -v "$1" >/dev/null 2>&1; }

load_cfg(){
  [[ -r "$CFG" ]] || return 0
  while IFS='=' read -r k v; do
    case "$k" in
      MODEL) MODEL="$v";;
      CONTEXT) CONTEXT="$v";;
      OUTPUT) OUTPUT="$v";;
      PORT) PORT="$v";;
      WEB) WEB="$v";;
      EGO) EGO="$v";;
      IDLE) IDLE="$v";;
      WEB_PORT) WEB_PORT="$v";;
    esac
  done < "$CFG"
}

load_auth(){
  [[ -r "$AUTH" ]] || return 0
  while IFS='=' read -r k v; do
    case "$k" in
      WEB_USERNAME) WEB_USERNAME="$v";;
      WEB_PASSWORD) WEB_PASSWORD="$v";;
    esac
  done < "$AUTH"
}

validate(){
  [[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo "ERROR: Apple Silicon macOS required" >&2; return 1; }
  case "$MODEL" in *[!A-Za-z0-9._/-]*|'') echo "ERROR: invalid model" >&2; return 1;; esac
  for n in "$CONTEXT" "$OUTPUT" "$PORT" "$IDLE" "$WEB_PORT"; do
    case "$n" in *[!0-9]*|'') echo "ERROR: invalid numeric setting" >&2; return 1;; esac
  done
  [[ "$PORT" -ge 1024 && "$PORT" -le 65535 ]] || { echo "ERROR: invalid API port" >&2; return 1; }
  [[ "$WEB_PORT" -ge 1024 && "$WEB_PORT" -le 65535 ]] || { echo "ERROR: invalid Web UI port" >&2; return 1; }
  [[ "$PORT" -ne "$WEB_PORT" ]] || { echo "ERROR: API and Web UI ports must differ" >&2; return 1; }
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
WEB_PORT=$WEB_PORT
EOF2
  chmod 600 "$CFG"
}

ensure_auth(){
  mkdirs
  load_auth
  if [[ -z "$WEB_PASSWORD" ]]; then
    WEB_PASSWORD="$(openssl rand -hex 18)"
    cat > "$AUTH" <<EOF2
WEB_USERNAME=$WEB_USERNAME
WEB_PASSWORD=$WEB_PASSWORD
EOF2
    chmod 600 "$AUTH"
  fi
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

write_mlx_plist(){
  local mlx idle=''
  mlx="$(command -v mlx-serve)"
  [[ "$IDLE" -gt 0 ]] && idle="<string>--idle-evict-secs</string><string>$IDLE</string>"
  cat > "$MLX_PLIST" <<EOF2
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$MLX_LABEL</string>
<key>ProgramArguments</key><array>
<string>$mlx</string><string>--serve</string><string>--model-dir</string><string>$MODEL_DIR</string>
<string>--host</string><string>$HOST</string><string>--port</string><string>$PORT</string>$idle
</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>$STATE/mlx-serve.log</string>
<key>StandardErrorPath</key><string>$STATE/mlx-serve-error.log</string>
</dict></plist>
EOF2
  plutil -lint "$MLX_PLIST" >/dev/null
}

write_web_runner(){
  cat > "$WEB_RUNNER" <<EOF2
#!/usr/bin/env bash
set -euo pipefail
source "$AUTH"
export OPENCODE_CONFIG="$OC_CFG"
export OPENCODE_CONFIG_CONTENT="\$(cat "$OC_CFG")"
EOF2
  if [[ "$WEB" == true ]]; then
    echo 'export OPENCODE_ENABLE_EXA=1' >> "$WEB_RUNNER"
  else
    echo 'unset OPENCODE_ENABLE_EXA 2>/dev/null || true' >> "$WEB_RUNNER"
  fi
  cat >> "$WEB_RUNNER" <<EOF2
export OPENCODE_SERVER_USERNAME="\$WEB_USERNAME"
export OPENCODE_SERVER_PASSWORD="\$WEB_PASSWORD"
exec "$(command -v opencode)" web --hostname "$WEB_HOST" --port "$WEB_PORT"
EOF2
  chmod 700 "$WEB_RUNNER"
}

write_web_plist(){
  cat > "$WEB_PLIST" <<EOF2
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$WEB_LABEL</string>
<key>ProgramArguments</key><array><string>$WEB_RUNNER</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>$STATE/opencode-web.log</string>
<key>StandardErrorPath</key><string>$STATE/opencode-web-error.log</string>
</dict></plist>
EOF2
  plutil -lint "$WEB_PLIST" >/dev/null
}

mlx_target(){ printf 'gui/%s/%s' "$(id -u)" "$MLX_LABEL"; }
web_target(){ printf 'gui/%s/%s' "$(id -u)" "$WEB_LABEL"; }
mlx_loaded(){ launchctl print "$(mlx_target)" >/dev/null 2>&1; }
web_loaded(){ launchctl print "$(web_target)" >/dev/null 2>&1; }
stop_old(){ launchctl bootout "gui/$(id -u)/$OLD_LABEL" >/dev/null 2>&1 || true; }

listener(){
  local port="$1"
  lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -n1
}

restart_mlx(){
  mlx_loaded && launchctl bootout "$(mlx_target)" >/dev/null 2>&1 || true
  stop_old
  local p c
  p="$(listener "$PORT")"
  if [[ -n "$p" ]]; then
    c="$(ps -p "$p" -o comm= 2>/dev/null || true)"
    if [[ "$c" == *mlx-serve* && -t 0 ]]; then
      printf 'Stop existing mlx-serve on port %s? [Y/n] ' "$PORT"
      read -r a || a=''
      [[ "$a" =~ ^[Nn] ]] || kill "$p" 2>/dev/null || true
      sleep 1
    fi
  fi
  [[ -z "$(listener "$PORT")" ]] || { echo "ERROR: port $PORT is already in use" >&2; return 1; }
  launchctl bootstrap "gui/$(id -u)" "$MLX_PLIST"
  launchctl kickstart -k "$(mlx_target)" >/dev/null
}

restart_web(){
  web_loaded && launchctl bootout "$(web_target)" >/dev/null 2>&1 || true
  local p
  p="$(listener "$WEB_PORT")"
  [[ -z "$p" ]] || { echo "ERROR: Web UI port $WEB_PORT is already in use" >&2; return 1; }
  launchctl bootstrap "gui/$(id -u)" "$WEB_PLIST"
  launchctl kickstart -k "$(web_target)" >/dev/null
}

restart_all(){
  restart_mlx
  wait_api || true
  restart_web
}

api(){ printf 'http://%s:%s' "$HOST" "$PORT"; }
wait_api(){
  for _ in {1..30}; do
    curl -fsS --max-time 2 "$(api)/v1/models" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

wait_web(){
  load_auth
  for _ in {1..30}; do
    curl -fsS --max-time 2 -u "$WEB_USERNAME:$WEB_PASSWORD" "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

remote_ip(){
  local ip
  ip="$(ifconfig 2>/dev/null | awk '/^[[:space:]]+inet / {print $2}' | grep -E '^100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.' | head -n1 || true)"
  [[ -n "$ip" ]] || ip="$(ipconfig getifaddr en0 2>/dev/null || true)"
  [[ -n "$ip" ]] || ip=localhost
  printf '%s' "$ip"
}

web_url(){ printf 'http://%s:%s' "$(remote_ip)" "$WEB_PORT"; }

web_access(){
  load_cfg
  load_auth
  echo
  echo "OpenCode Web"
  echo
  printf '  %-18s %s\n' URL "$(web_url)"
  printf '  %-18s %s\n' username "$WEB_USERNAME"
  printf '  %-18s %s\n' password "${WEB_PASSWORD:-not-generated}"
  echo
}

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
  case ":$PATH:" in
    *":$BIN:"*) ;;
    *)
      touch "$ZSHRC"
      remove_block
      printf '\n%s\nexport PATH="$HOME/.local/bin:$PATH"\n%s\n' "$BEGIN" "$END" >> "$ZSHRC"
      export PATH="$BIN:$PATH"
      ;;
  esac
}

install_stack(){
  have brew || { echo "ERROR: install Homebrew first: https://brew.sh" >&2; return 1; }
  mkdirs
  load_cfg
  validate
  echo
  echo "Installing local AI stack..."
  echo

  if ! have mlx-serve; then
    brew tap ddalcu/mlx-serve https://github.com/ddalcu/mlx-serve
    if brew help trust >/dev/null 2>&1; then
      brew trust --formula ddalcu/mlx-serve/mlx-serve
    fi
    brew install ddalcu/mlx-serve/mlx-serve
  else
    echo "  ✓ mlx-serve installed"
  fi

  if ! have opencode; then
    brew install anomalyco/tap/opencode
  else
    echo "  ✓ OpenCode installed"
  fi

  mlx-serve list 2>/dev/null | grep -Fq "$MODEL" || mlx-serve pull "$MODEL"

  save_cfg
  ensure_auth
  write_oc
  write_mlx_plist
  write_web_runner
  write_web_plist
  install_cmd
  restart_all

  wait_api && echo "  ✓ API ready at $(api)" || echo "  WARNING: API not ready yet"
  wait_web && echo "  ✓ OpenCode Web ready at $(web_url)" || echo "  WARNING: OpenCode Web not ready yet"

  echo
  echo "✓ Installed"
  echo
  echo "Run: local-ai"
  echo "Web: local-ai web"
  echo
}

status(){
  load_cfg
  load_auth
  echo
  echo "Local AI status"
  echo
  printf '  %-18s %s\n' mlx-serve "$(have mlx-serve && mlx-serve --version 2>/dev/null | head -n1 || echo not-installed)"
  printf '  %-18s %s\n' "mlx service" "$(mlx_loaded && echo running || echo stopped)"
  printf '  %-18s %s\n' API "$(curl -fsS --max-time 2 "$(api)/v1/models" >/dev/null 2>&1 && echo "$(api)" || echo unreachable)"
  printf '  %-18s %s\n' model "$MODEL"
  printf '  %-18s %s\n' OpenCode "$(have opencode && opencode --version 2>/dev/null | head -n1 || echo not-installed)"
  printf '  %-18s %s\n' "Web service" "$(web_loaded && echo running || echo stopped)"
  if [[ -n "$WEB_PASSWORD" ]] && curl -fsS --max-time 2 -u "$WEB_USERNAME:$WEB_PASSWORD" "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1; then
    printf '  %-18s %s\n' "Web UI" "$(web_url)"
  else
    printf '  %-18s %s\n' "Web UI" unreachable
  fi
  printf '  %-18s %s\n' web-tools "$WEB"
  printf '  %-18s %s\n' ego-browser "$(have ego-browser && echo ready || echo not-ready)"
  echo
}

oc_env(){
  export OPENCODE_CONFIG="$OC_CFG"
  export OPENCODE_CONFIG_CONTENT="$(cat "$OC_CFG")"
  [[ "$WEB" == true ]] && export OPENCODE_ENABLE_EXA=1 || unset OPENCODE_ENABLE_EXA 2>/dev/null || true
}

launch_oc(){
  load_cfg
  [[ -r "$OC_CFG" ]] || { echo "Run Install / update first" >&2; return 1; }
  oc_env
  opencode "$@"
}

test_stack(){
  load_cfg
  load_auth
  local fail=0
  echo
  echo "Testing local AI stack..."
  echo

  have mlx-serve && echo "  ✓ mlx-serve" || { echo "  ✗ mlx-serve"; fail=1; }
  mlx_loaded && echo "  ✓ mlx launchd service" || { echo "  ✗ mlx launchd service"; fail=1; }
  curl -fsS --max-time 3 "$(api)/v1/models" >/dev/null 2>&1 && echo "  ✓ API" || { echo "  ✗ API"; fail=1; }
  have opencode && echo "  ✓ OpenCode" || { echo "  ✗ OpenCode"; fail=1; }
  [[ -r "$OC_CFG" ]] && echo "  ✓ OpenCode config" || { echo "  ✗ OpenCode config"; fail=1; }
  web_loaded && echo "  ✓ OpenCode Web service" || { echo "  ✗ OpenCode Web service"; fail=1; }
  if [[ -n "$WEB_PASSWORD" ]] && curl -fsS --max-time 3 -u "$WEB_USERNAME:$WEB_PASSWORD" "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1; then
    echo "  ✓ OpenCode Web"
  else
    echo "  ✗ OpenCode Web"
    fail=1
  fi

  if [[ "$fail" -eq 0 ]]; then
    local r
    r="$(curl -fsS --max-time 180 -H 'Content-Type: application/json' -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply exactly: local-ai-ok\"}],\"max_tokens\":16,\"temperature\":0}" "$(api)/v1/chat/completions" 2>/dev/null || true)"
    printf '%s' "$r" | grep -Fq local-ai-ok && echo "  ✓ model inference" || { echo "  ✗ model inference"; fail=1; }
  fi

  [[ "$WEB" == true ]] && echo "  ✓ websearch/webfetch configured" || echo "  - web tools disabled"
  echo
  [[ "$fail" -eq 0 ]] && echo "✓ Everything looks good." || echo "Some checks failed."
  echo
  return "$fail"
}

prompt_num(){
  local label="$1" cur="$2" min="$3" max="$4" v
  printf '%s [%s]: ' "$label" "$cur"
  read -r v || v=''
  v="${v:-$cur}"
  case "$v" in *[!0-9]*|'') return 1;; esac
  [[ "$v" -ge "$min" && "$v" -le "$max" ]] || return 1
  printf '%s' "$v"
}

apply(){
  validate
  save_cfg
  ensure_auth
  write_oc
  write_mlx_plist
  write_web_runner
  write_web_plist
  restart_all
  wait_api || true
  wait_web || true
}

configure(){
  load_cfg
  while true; do
    clear
    echo "LOCAL AI · Configure"
    echo
    echo " 1  Model               $MODEL"
    echo " 2  Context             $CONTEXT"
    echo " 3  Output tokens       $OUTPUT"
    echo " 4  Web tools           $WEB"
    echo " 5  Browser automation  $EGO"
    echo " 6  API port            $PORT"
    echo " 7  Web UI port         $WEB_PORT"
    echo " 8  Idle eviction       $IDLE"
    echo " 9  Back"
    echo
    printf 'choose ❯ '
    read -r c || c=9

    case "$c" in
      1)
        mlx-serve list || true
        printf 'Model [%s]: ' "$MODEL"
        read -r v || v=''
        [[ -n "$v" ]] && MODEL="$v"
        mlx-serve list 2>/dev/null | grep -Fq "$MODEL" || mlx-serve pull "$MODEL"
        apply
        ;;
      2) v="$(prompt_num Context "$CONTEXT" 4096 1048576)" && CONTEXT="$v" && apply || true;;
      3) v="$(prompt_num 'Output tokens' "$OUTPUT" 256 262144)" && OUTPUT="$v" && apply || true;;
      4) [[ "$WEB" == true ]] && WEB=false || WEB=true; apply;;
      5)
        if [[ "$EGO" == true ]]; then
          EGO=false
          apply
        else
          have npx || {
            printf 'Install Node.js? [y/N] '
            read -r a || a=''
            [[ "$a" =~ ^[Yy] ]] && brew install node || continue
          }
          npx skills add citrolabs/ego-lite
          EGO=true
          apply
        fi
        ;;
      6) v="$(prompt_num 'API port' "$PORT" 1024 65535)" && PORT="$v" && apply || true;;
      7) v="$(prompt_num 'Web UI port' "$WEB_PORT" 1024 65535)" && WEB_PORT="$v" && apply || true;;
      8) v="$(prompt_num 'Idle seconds' "$IDLE" 0 86400)" && IDLE="$v" && apply || true;;
      9) return;;
    esac

    printf '\n[enter] '
    read -r _ || true
  done
}

uninstall_stack(){
  printf 'Remove local-ai managed files? [y/N] '
  read -r a || a=''
  [[ "$a" =~ ^[Yy] ]] || return 0

  mlx_loaded && launchctl bootout "$(mlx_target)" >/dev/null 2>&1 || true
  web_loaded && launchctl bootout "$(web_target)" >/dev/null 2>&1 || true

  rm -f "$MLX_PLIST" "$WEB_PLIST" "$CMD"
  remove_block
  rm -rf "$CFG_DIR" "$STATE"
  rm -f "$INSTALLED" "$WEB_RUNNER"
  rmdir "$SHARE" 2>/dev/null || true

  echo "Removed local-ai configuration and services. Models and installed packages were kept."
}

menu(){
  while true; do
    clear
    echo "LOCAL AI · MAC MINI"
    echo "OpenCode + mlx-serve + MLX"
    echo
    echo " 1  Launch OpenCode"
    echo " 2  OpenCode Web access"
    echo " 3  Install / update"
    echo " 4  Configure"
    echo " 5  Status"
    echo " 6  Test"
    echo " 7  Restart services"
    echo " 8  Uninstall"
    echo " 9  Quit"
    echo
    printf 'choose ❯ '
    read -r c || c=9

    case "$c" in
      1) launch_oc;;
      2) web_access; read -r _ || true;;
      3) install_stack; read -r _ || true;;
      4) configure;;
      5) status; read -r _ || true;;
      6) test_stack || true; read -r _ || true;;
      7) restart_all; read -r _ || true;;
      8) uninstall_stack; return;;
      9) return;;
    esac
  done
}

main(){
  load_cfg
  case "${1:-}" in
    opencode|launch) shift; launch_oc "$@";;
    web) web_access;;
    status) status;;
    test) test_stack;;
    restart) restart_all;;
    help|-h|--help) echo "local-ai [opencode|web|status|test|restart]";;
    '') [[ -t 0 && -t 1 ]] && menu || install_stack;;
    *) echo "local-ai [opencode|web|status|test|restart]" >&2; return 2;;
  esac
}

main "$@"
