# Local AI Mac mini

A self-contained local coding agent for Apple Silicon Macs.

This repository combines OpenCode, mlx-serve, Qwen and Apple MLX into a local coding environment that can work directly inside your repositories.

It can inspect and edit files, run shell commands, build projects, run tests and work with Git. It also runs OpenCode Web as a persistent background service so the coding environment can be used from another machine in a browser. Optional web search and browser automation can be enabled when external access is useful.

The included `local-ai` command handles installation, configuration, background services, diagnostics and daily use.

## What it does

- runs Qwen locally on Apple Silicon with MLX
- uses OpenCode as the coding-agent interface
- gives the agent access to repositories, files, shell commands, builds, tests and Git
- runs OpenCode Web persistently for browser access from another machine
- starts both `mlx-serve` and OpenCode Web automatically with `launchd`
- keeps the local model API bound to `127.0.0.1`
- protects remote OpenCode Web access with a generated password
- keeps this stack's OpenCode configuration separate from your normal OpenCode config
- supports optional web search and web fetching
- supports optional browser automation with ego lite
- provides one `local-ai` command for setup, configuration, status, testing, restart and daily use

## Architecture

```text
Browser on another machine
          │
          ▼
OpenCode Web :4096
          │
          ├── repository and file access
          ├── shell commands
          ├── builds and tests
          ├── Git
          ├── optional websearch / webfetch
          └── optional ego-browser automation
          │
          ▼
mlx-serve :11234
          │
          ▼
Qwen3.5 9B 6-bit
          │
          ▼
Apple MLX / Metal
```

The language model runs locally. `mlx-serve` listens only on `127.0.0.1`. OpenCode Web listens on the network with password authentication so it can be reached from another machine.

## Features

- local Qwen inference using Apple MLX
- agentic coding through OpenCode
- persistent OpenCode Web interface
- automatic background startup with `launchd`
- generated password for remote Web UI access
- menu-driven setup and configuration
- isolated OpenCode configuration
- configurable model, context size and output-token limit
- configurable OpenCode Web port
- optional idle model eviction
- optional web search and web fetching
- optional ego lite browser automation
- built-in status, testing, restart and uninstall

## Usage

For terminal use, open a repository and start `local-ai`:

```bash
cd ~/source/repos/my-project
local-ai
```

The menu is:

```text
LOCAL AI · MAC MINI
OpenCode + mlx-serve + MLX

 1  Launch OpenCode
 2  OpenCode Web access
 3  Install / update
 4  Configure
 5  Status
 6  Test
 7  Restart services
 8  Uninstall
 9  Quit
```

Choose **Launch OpenCode** to start the terminal interface in the current directory.

You can also launch it directly:

```bash
local-ai opencode
```

For browser access from another machine:

```bash
local-ai web
```

This prints the current Web UI URL, username and generated password.

Quick status:

```bash
local-ai status
```

Run the built-in checks:

```bash
local-ai test
```

## OpenCode Web

OpenCode Web runs continuously as a per-user LaunchAgent.

Default port:

```text
4096
```

The service binds to `0.0.0.0` so it can be reached through a LAN or overlay network such as NetBird. `local-ai web` prefers a `100.64.0.0/10` overlay address when it finds one and otherwise falls back to the Mac's primary network address.

Access details:

```bash
local-ai web
```

Example:

```text
OpenCode Web

  URL                http://100.x.x.x:4096
  username           opencode
  password           <generated password>
```

The password is generated during installation and stored locally with mode `600` in:

```text
~/.config/local-ai-mac-mini/web-auth
```

OpenCode uses HTTP Basic Authentication. The username defaults to `opencode`.

The Web UI uses the same managed local Qwen/MLX configuration as terminal sessions started through `local-ai`.

## Configuration

The managed configuration lives in:

```text
~/.config/local-ai-mac-mini/
├── config
├── opencode.json
└── web-auth
```

`local-ai` uses `OPENCODE_CONFIG` and a matching runtime override instead of replacing your normal:

```text
~/.config/opencode/opencode.json
```

Existing OpenCode providers and personal configuration are therefore left alone. OpenCode launched through `local-ai`, including the persistent Web UI, allowlists only the local `mlx` provider, so it cannot silently switch to a cloud LLM because of another OpenCode configuration.

The Configure menu can change:

- model
- context size
- output-token limit
- web tools
- ego-browser integration
- local MLX API port
- OpenCode Web port
- idle model eviction

Default values:

```text
Model:          mlx-community/Qwen3.5-9B-6bit
Context:        131072
Output tokens:  16384
MLX API:        127.0.0.1:11234
OpenCode Web:   0.0.0.0:4096
Web tools:      enabled
Idle eviction:  off
```

## Install

Requirements:

- Apple Silicon Mac
- Homebrew
- macOS compatible with the current `mlx-serve` release

Clone the repository and run the setup script:

```bash
git clone https://github.com/Carlboms-Data-AB/local-ai-mac-mini.git
cd local-ai-mac-mini
./setup.sh
```

The installer opens the menu. Choose **Install / update**.

It installs or configures:

- `mlx-serve`
- OpenCode
- `mlx-community/Qwen3.5-9B-6bit`
- a dedicated OpenCode configuration for this stack
- a `launchd` service for `mlx-serve`
- a `launchd` service for OpenCode Web
- password-protected remote Web UI access
- the `local-ai` management command

After installation:

```bash
local-ai
```

If the command is not visible in the shell that ran the installer yet:

```bash
source ~/.zshrc
```

## Background services

The installer creates two LaunchAgents:

```text
~/Library/LaunchAgents/se.carlbomsdata.local-ai-mlx-serve.plist
~/Library/LaunchAgents/se.carlbomsdata.local-ai-opencode-web.plist
```

The MLX service runs approximately:

```bash
mlx-serve \
  --serve \
  --model-dir ~/.mlx-serve/models \
  --host 127.0.0.1 \
  --port 11234
```

The OpenCode service runs approximately:

```bash
opencode web \
  --hostname 0.0.0.0 \
  --port 4096
```

Its environment contains the managed OpenCode configuration and the locally generated Web UI credentials.

The model is loaded on demand. If idle eviction is enabled in **Configure**, the model is unloaded after the configured idle period.

These are per-user LaunchAgents. They start automatically when that macOS user session is logged in; they are not pre-login system daemons.

You do not need to run either service manually.

Logs are stored in:

```text
~/.local/state/local-ai-mac-mini/
```

## Web search

Web tools are enabled by default.

When OpenCode is launched through `local-ai`, including OpenCode Web, the launcher sets:

```text
OPENCODE_ENABLE_EXA=1
```

The managed OpenCode config also permits:

- `websearch`
- `webfetch`

The LLM inference remains local, but web searches are sent to Exa and `webfetch` contacts the requested website.

Disable web tools from:

```text
local-ai → Configure → Web tools
```

## Browser automation with ego lite

Browser automation is optional and disabled by default.

Enable it from:

```text
local-ai → Configure → Browser automation
```

The setup installs the `ego-browser` skill with:

```bash
npx skills add citrolabs/ego-lite
```

ego lite also requires its macOS application and one-time GUI onboarding. The browser integration is intentionally opt-in because it can use real browser state and authenticated sessions.

## Test

Choose:

```text
local-ai → Test
```

The test checks:

- `mlx-serve`
- the MLX LaunchAgent
- the local MLX API
- the configured model
- OpenCode
- the managed OpenCode config
- the OpenCode Web LaunchAgent
- authenticated access to OpenCode Web
- a small local inference request
- web/ego configuration

## Update

To apply a newer version of this repository:

```bash
cd ~/source/repos/local-ai-mac-mini
git pull
./setup.sh
```

Choose **Install / update**. The updated script is copied into the installed `local-ai` command and both background services are recreated.

The installer is idempotent. Existing downloaded models and already installed programs are reused. The local configuration is kept separately so replacing `setup.sh` does not reset your choices.

## Uninstall

Choose:

```text
local-ai → Uninstall
```

This removes only the files managed by this project:

- both LaunchAgents
- its OpenCode configuration
- its local configuration and generated Web UI credentials
- its logs
- its `~/.zshrc` PATH block, if one was added
- the `local-ai` command

It deliberately keeps:

- downloaded MLX models
- `mlx-serve`
- OpenCode
- ego lite / ego-browser

Uninstalling `local-ai` therefore does **not** delete the downloaded Qwen model.

## Migrating from an earlier manual setup

The managed installer uses different paths from an earlier manual setup:

```text
Managed MLX LaunchAgent:
~/Library/LaunchAgents/se.carlbomsdata.local-ai-mlx-serve.plist

Managed OpenCode Web LaunchAgent:
~/Library/LaunchAgents/se.carlbomsdata.local-ai-opencode-web.plist

Managed OpenCode config:
~/.config/local-ai-mac-mini/opencode.json
```

If the older `se.carlbomsdata.mlx-serve` LaunchAgent is loaded, the installer stops it before starting the managed services. It leaves the old plist and normal global OpenCode config untouched so they can be removed only after the new stack has been tested.

A manually started `mlx-serve` process on the configured port is also detected. In an interactive install, the setup offers to stop it.

## Files

| File | Role |
|---|---|
| `setup.sh` | installer, configuration UI and local management command |
| `README.md` | project overview, installation and usage |
| `.gitattributes` | cross-platform line-ending policy |
| `.gitignore` | repository-local ignore rules |

## Security

- `mlx-serve` binds to `127.0.0.1`, not the LAN.
- OpenCode Web requires a generated password for network access.
- Web UI credentials are stored only in the local user configuration with mode `600`.
- OpenCode configuration is isolated from the user's normal global config.
- Web access is explicit and can be disabled.
- Browser automation is opt-in.
- The installer does not modify SSH, GPG, Git remotes or Git configuration.
