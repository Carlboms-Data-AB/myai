# Local AI Mac mini

A menu-driven local coding-agent setup for Apple Silicon Macs.

```text
OpenCode
   │
   ├── repository, shell, Git, builds and tests
   ├── websearch / webfetch (optional external web access)
   └── ego-browser (optional browser automation)
   │
   ▼
mlx-serve
   │
   ▼
Qwen3.5 9B 6-bit
   │
   ▼
Apple MLX / Metal
```

The model runs locally. `mlx-serve` listens only on `127.0.0.1`.

## Install

Requirements:

- Apple Silicon Mac
- Homebrew
- macOS compatible with the current `mlx-serve` release

Clone this private repository using your normal GitHub authentication, then run:

```bash
git clone git@github.com:Carlboms-Data-AB/local-ai-mac-mini.git
cd local-ai-mac-mini
./setup.sh
```

The installer opens a menu. Choose **Install / update**.

It installs or configures:

- `mlx-serve`
- OpenCode
- `mlx-community/Qwen3.5-9B-6bit`
- a private OpenCode configuration for this stack
- a `launchd` service for `mlx-serve`
- the `local-ai` management command

After installation:

```bash
local-ai
```

If the command is not visible in the shell that ran the installer yet:

```bash
source ~/.zshrc
```

## Menu

```text
┌──────────────────────────────────┐
│  LOCAL AI · MAC MINI             │
│  OpenCode + mlx-serve + MLX      │
└──────────────────────────────────┘

 1  Launch OpenCode
 2  Install / update
 3  Configure
 4  Status
 5  Test
 6  Restart mlx-serve
 7  Uninstall
 8  Quit
```

Normal use is simply:

```bash
cd ~/source/repos/my-project
local-ai
```

Choose **Launch OpenCode**. OpenCode starts in the current directory.

You can also launch it directly:

```bash
local-ai opencode
```

## Configuration

The managed configuration lives in:

```text
~/.config/local-ai-mac-mini/
├── config
└── opencode.json
```

`local-ai` deliberately uses `OPENCODE_CONFIG` and a matching runtime override instead of replacing your normal:

```text
~/.config/opencode/opencode.json
```

Existing OpenCode providers and personal configuration are therefore left alone. The managed config is also applied at runtime and allowlists only the local `mlx` provider, so OpenCode launched through `local-ai` cannot silently switch to a cloud LLM even when a project contains its own OpenCode config.

The Configure menu can change:

- model
- context size
- output-token limit
- web tools
- ego-browser integration
- local API port
- idle model eviction

Default values:

```text
Model:          mlx-community/Qwen3.5-9B-6bit
Context:        131072
Output tokens:  16384
API:            127.0.0.1:11234
Web tools:      enabled
Idle eviction:  off
```

## Background service

The installer creates its own LaunchAgent:

```text
~/Library/LaunchAgents/se.carlbomsdata.local-ai-mlx-serve.plist
```

It runs approximately:

```bash
mlx-serve \
  --serve \
  --model-dir ~/.mlx-serve/models \
  --host 127.0.0.1 \
  --port 11234
```

The model is loaded on demand. If idle eviction is enabled in **Configure**, the model is also unloaded after the configured idle period.

This is a per-user LaunchAgent. It starts automatically when that macOS user session is logged in; it is not a pre-login system daemon.

You do not need to run `mlx-serve serve` manually.

Logs are stored in:

```text
~/.local/state/local-ai-mac-mini/
```


## Migrating from the earlier manual setup

The managed installer intentionally uses different paths from the earlier manual setup:

```text
Managed LaunchAgent:
~/Library/LaunchAgents/se.carlbomsdata.local-ai-mlx-serve.plist

Managed OpenCode config:
~/.config/local-ai-mac-mini/opencode.json
```

If the older `se.carlbomsdata.mlx-serve` LaunchAgent is loaded, the installer stops it before starting the managed service. It leaves the old plist and the normal global OpenCode config untouched so they can be removed only after the new stack has been tested.

A manually started `mlx-serve` process on the configured port is also detected; in an interactive install, the setup offers to stop it.

## Web search

Web tools are enabled by default.

When OpenCode is launched through `local-ai`, the launcher sets:

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

ego lite also requires its macOS application and one-time GUI onboarding. The first browser task can guide you through that part.

The browser integration is intentionally opt-in because it can use real browser state and authenticated sessions.

## Test

Choose:

```text
local-ai → Test
```

The test checks:

- `mlx-serve`
- the LaunchAgent
- the local API
- the configured model
- OpenCode
- the managed OpenCode config
- a small local inference request
- web/ego configuration


## Status

For a quick status screen:

```bash
local-ai status
```

For the interactive menu:

```bash
local-ai
```

## Update

To apply a newer version of this repository:

```bash
cd ~/source/repos/local-ai-mac-mini
git pull
./setup.sh
```

Choose **Install / update**. The updated script is copied into the installed `local-ai` command.

The installer is idempotent. Existing downloaded models and already installed programs are reused.

The local configuration is kept in a separate file so replacing `setup.sh` does not reset your choices.

## Uninstall

Choose:

```text
local-ai → Uninstall
```

This removes only the files managed by this project:

- its LaunchAgent
- its OpenCode configuration
- its local configuration
- its logs
- its `~/.zshrc` PATH block, if one was added
- the `local-ai` command

It deliberately keeps:

- downloaded MLX models
- `mlx-serve`
- OpenCode
- ego lite / ego-browser

Those may be shared with other workflows and are not removed automatically.

## Files

| File | Role |
|---|---|
| `setup.sh` | installer, configuration UI and local management command |
| `README.md` | installation and usage |
| `.gitattributes` | cross-platform line-ending policy |
| `.gitignore` | repository-local ignore rules |

## Security

- `mlx-serve` binds to `127.0.0.1`, not the LAN.
- The project does not store API keys or other secrets.
- OpenCode configuration is isolated from the user's normal global config.
- Web access is explicit and can be disabled.
- Browser automation is opt-in.
- The installer does not modify SSH, GPG, Git remotes or Git configuration.
