# local-ai-mac-mini

# Local Coding Agent on Apple Silicon

This guide sets up a fully local coding agent on an Apple Silicon Mac using:

* **OpenCode** as the coding-agent interface
* **mlx-serve** as the local inference server
* **Qwen3.5 9B 6-bit** as the local LLM
* **MLX** for native Apple Silicon inference
* **launchd** to run `mlx-serve` automatically in the background

The LLM runs locally. OpenCode gets access to the repository, shell, files, builds, tests, Git, etc.

## Architecture

```text
OpenCode
   │
   │ OpenAI-compatible API
   ▼
mlx-serve
   │
   ▼
Qwen3.5 9B 6-bit
   │
   ▼
Apple MLX / Metal
```

The local API is bound only to:

```text
127.0.0.1:11234
```

It is therefore not exposed to the LAN or Internet.

---

# 1. Prerequisites

Install Homebrew if it is not already installed.

Verify:

```bash
brew --version
```

This setup does **not** require:

* Ollama
* Docker
* Python
* Open WebUI
* Goose
* Claude Code

---

# 2. Install mlx-serve

Add the Homebrew tap:

```bash
brew tap ddalcu/mlx-serve
```

Recent Homebrew versions may require explicit trust for third-party taps.

Trust only the `mlx-serve` formula:

```bash
brew trust --formula ddalcu/mlx-serve/mlx-serve
```

Install it:

```bash
brew install mlx-serve
```

Verify:

```bash
mlx-serve --version
```

You should see output similar to:

```text
mlx-serve 26.x.x
mlx 0.x.x
```

---

# 3. Download the model

Download the MLX-optimized Qwen model:

```bash
mlx-serve pull mlx-community/Qwen3.5-9B-6bit
```

Verify:

```bash
mlx-serve list
```

Expected:

```text
NAME                                  TYPE    SIZE
mlx-community/Qwen3.5-9B-6bit         chat    ~7.6 GB
```

This model is a good fit for a 24 GB Apple Silicon Mac because it leaves significant memory available for:

* KV cache
* coding-agent context
* macOS
* OpenCode
* build tools

---

# 4. Test mlx-serve manually

Before configuring the background service, test the server manually:

```bash
mlx-serve \
  --serve \
  --model-dir "$HOME/.mlx-serve/models" \
  --host 127.0.0.1 \
  --port 11234
```

Leave this terminal running temporarily.

From another terminal:

```bash
curl 127.0.0.1:11234/v1/models
```

The response should contain:

```text
mlx-community/Qwen3.5-9B-6bit
```

The model may initially show:

```json
"loaded": false
```

That is normal.

Models are loaded into memory on demand when the first inference request arrives.

Stop the temporary server with:

```text
Ctrl+C
```

---

# 5. Run mlx-serve automatically in the background

Create a macOS LaunchAgent:

```bash
mkdir -p "$HOME/Library/LaunchAgents"
mkdir -p "$HOME/.mlx-serve"
```

Create the service:

```bash
cat > "$HOME/Library/LaunchAgents/local.mlx-serve.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>local.mlx-serve</string>

    <key>ProgramArguments</key>
    <array>
        <string>/opt/homebrew/bin/mlx-serve</string>
        <string>--serve</string>
        <string>--model-dir</string>
        <string>${HOME}/.mlx-serve/models</string>
        <string>--host</string>
        <string>127.0.0.1</string>
        <string>--port</string>
        <string>11234</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>${HOME}/.mlx-serve/server.log</string>

    <key>StandardErrorPath</key>
    <string>${HOME}/.mlx-serve/server-error.log</string>
</dict>
</plist>
EOF
```

Validate the plist:

```bash
plutil -lint "$HOME/Library/LaunchAgents/local.mlx-serve.plist"
```

Expected:

```text
OK
```

Load it:

```bash
launchctl bootstrap \
  gui/$(id -u) \
  "$HOME/Library/LaunchAgents/local.mlx-serve.plist"
```

Start it immediately:

```bash
launchctl kickstart -k \
  gui/$(id -u)/local.mlx-serve
```

Verify:

```bash
curl 127.0.0.1:11234/v1/models
```

mlx-serve is now running in the background.

You no longer need to manually run:

```bash
mlx-serve serve
```

## Important: LaunchAgent behavior

This is a **per-user LaunchAgent**.

It starts automatically when that macOS user session is logged in.

It does not run at the FileVault/login screen before the user has logged in.

---

# 6. Check service status

Check the service:

```bash
launchctl print gui/$(id -u)/local.mlx-serve
```

Check the API:

```bash
curl 127.0.0.1:11234/v1/models
```

View logs:

```bash
tail -f "$HOME/.mlx-serve/server.log"
```

Errors:

```bash
tail -f "$HOME/.mlx-serve/server-error.log"
```

---

# 7. Install OpenCode

The simple Homebrew installation is:

```bash
brew install opencode
```

Verify:

```bash
opencode --version
```

If the newest OpenCode release is required, the upstream OpenCode Homebrew tap can be used instead.

---

# 8. Configure OpenCode for mlx-serve

Create the OpenCode configuration directory:

```bash
mkdir -p "$HOME/.config/opencode"
```

Create:

```text
~/.config/opencode/opencode.json
```

Use:

```bash
cat > "$HOME/.config/opencode/opencode.json" <<'EOF'
{
  "model": "mlx/mlx-community/Qwen3.5-9B-6bit",
  "provider": {
    "mlx": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "MLX Serve (local)",
      "options": {
        "baseURL": "http://127.0.0.1:11234/v1"
      },
      "models": {
        "mlx-community/Qwen3.5-9B-6bit": {
          "name": "Qwen3.5 9B 6-bit",
          "limit": {
            "context": 131072,
            "output": 16384
          }
        }
      }
    }
  }
}
EOF
```

Validate the JSON:

```bash
python3 -m json.tool "$HOME/.config/opencode/opencode.json"
```

There should be no JSON errors.

---

# 9. Verify the OpenCode model

Start OpenCode:

```bash
opencode
```

The model indicator should show something similar to:

```text
Qwen3.5 9B 6-bit
MLX Serve (local)
```

It should **not** show an OpenCode cloud model such as:

```text
Big Pickle
OpenCode Zen
```

---

# 10. Use it with a repository

Clone or open a project:

```bash
mkdir -p "$HOME/source/repos"
cd "$HOME/source/repos"

git clone <repository>
cd <repository>
```

Start the agent:

```bash
opencode
```

OpenCode now operates from that repository directory.

It can use tools to:

* inspect files
* search the codebase
* edit files
* create files
* run shell commands
* run builds
* run tests
* use Git
* inspect failures
* iterate on changes

Example:

```text
Investigate this repository and explain its architecture.
Do not modify anything yet.
```

Or:

```text
Find the cause of the failing tests.
Fix the problem and run the test suite again.
```

---

# 11. Verify tool calling

A simple test:

```text
Use the Bash tool to run pwd. Show me the actual output.
```

You should see a real command execution.

For a .NET repository:

```text
Find the existing C# project in this directory.
Run it using dotnet run.
Do not modify any files.
Show me the actual terminal output.
```

A successful agent run should contain actual shell execution such as:

```bash
dotnet run --project MyProject/MyProject.csproj
```

and the real stdout from the process.

---

# 12. Memory behavior

The model does **not** have to remain permanently loaded in unified memory.

mlx-serve runs continuously as a lightweight server, while models from:

```text
~/.mlx-serve/models
```

are loaded on demand.

Check available models:

```bash
curl 127.0.0.1:11234/v1/models
```

Look for:

```json
"loaded": true
```

when the model is resident.

---

# 13. Security

Keep mlx-serve bound to:

```text
127.0.0.1
```

Do not use:

```text
0.0.0.0
```

unless remote API access is intentionally required.

OpenCode runs on the same Mac, so there is no reason to expose port `11234`.

The architecture should remain:

```text
OpenCode
    ↓
127.0.0.1:11234
    ↓
mlx-serve
    ↓
local MLX model
```

No LLM API port needs to be exposed through the router or VPN.

---

# 14. Restart mlx-serve

Restart the background server:

```bash
launchctl kickstart -k \
  gui/$(id -u)/local.mlx-serve
```

If the plist itself was changed, unload and reload it:

```bash
launchctl bootout \
  gui/$(id -u) \
  "$HOME/Library/LaunchAgents/local.mlx-serve.plist"

launchctl bootstrap \
  gui/$(id -u) \
  "$HOME/Library/LaunchAgents/local.mlx-serve.plist"
```

---

# 15. Update

Update mlx-serve:

```bash
brew update
brew upgrade mlx-serve
```

Update OpenCode:

```bash
brew upgrade opencode
```

Restart mlx-serve after upgrading:

```bash
launchctl kickstart -k \
  gui/$(id -u)/local.mlx-serve
```

---

# 16. Useful diagnostics

Check installed models:

```bash
mlx-serve list
```

Check API:

```bash
curl 127.0.0.1:11234/v1/models
```

Check mlx-serve process:

```bash
ps aux | grep '[m]lx-serve'
```

Check memory:

```bash
memory_pressure
```

Check system load:

```bash
btop
```

Check OpenCode configuration:

```bash
cat "$HOME/.config/opencode/opencode.json"
```

Validate configuration:

```bash
python3 -m json.tool "$HOME/.config/opencode/opencode.json"
```

---

# 17. Complete stack

The finished setup is:

```text
Headless Apple Silicon Mac
│
├── SSH / remote administration
│
├── launchd
│   └── mlx-serve
│       ├── listens only on 127.0.0.1:11234
│       └── loads models on demand
│
├── MLX model
│   └── mlx-community/Qwen3.5-9B-6bit
│
└── OpenCode
    ├── repository access
    ├── file editing
    ├── search
    ├── shell commands
    ├── Git
    ├── builds
    └── tests
```

For normal use:

```bash
cd ~/source/repos/my-project
opencode
```

Nothing else needs to be manually started.
