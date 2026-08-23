# MyAI

MyAI is a local coding agent stack. It runs a language model on your own
machine, points OpenCode at it, and keeps the whole thing alive as a
background service you can reach from a browser on another computer.

The model itself never leaves your machine. There is no cloud provider in the
loop, and the OpenCode configuration MyAI manages allows only the local model,
so a session cannot quietly fall back to a hosted LLM.

Web search and web fetching are off by default, so out of the box nothing at
all leaves the machine. Turning them on under **Configure** keeps inference
local but sends search queries to Exa and lets fetches reach the site being
requested.

The `myai` command handles installation, models, configuration, background
services, diagnostics and daily use. It runs natively on macOS, Windows and
Linux, including Windows ARM64. It never uses WSL.

## What it does

- runs a language model locally, on Apple Silicon through MLX and on Windows
  and Linux through llama.cpp
- runs OpenCode in the directory you start it in, where it can read and edit
  files, run shell commands, builds and tests, and use Git
- serves the OpenCode Web interface as a background service so you can work
  from a browser on another machine
- keeps the model loaded in memory, or unloads it after an idle period, as you
  prefer
- downloads, lists, switches and deletes models without you having to know
  what format your platform needs
- keeps its OpenCode configuration separate from your personal one

## Architecture

```text
Browser on another machine
          │
          ▼
   OpenCode Web  :4096
          │
          ├── files in the working directory
          ├── shell commands
          ├── builds and tests
          ├── Git
          ├── optional web search and fetching
          └── optional browser automation
          │
          ▼
   inference API  127.0.0.1:11234
          │
          ▼
   ┌──────────────┬─────────────────────┐
   │ macOS        │ Windows and Linux   │
   │ mlx-serve    │ llama.cpp           │
   │ MLX model    │ GGUF model          │
   │ Metal        │ Vulkan, CUDA or CPU │
   └──────────────┴─────────────────────┘
```

The inference API binds to `127.0.0.1` and is not exposed to the network.
OpenCode Web is the only part that listens beyond loopback, and it requires a
password when it does.

You choose a model by name. MyAI resolves that name to whichever artifact your
platform actually needs:

| Logical model | Apple Silicon | Windows and Linux |
|---|---|---|
| Qwen3.5 9B | `mlx-community/Qwen3.5-9B-6bit` | `unsloth/Qwen3.5-9B-GGUF` → `Qwen3.5-9B-Q6_K.gguf` |
| Qwen3.5 9B Compact | `mlx-community/Qwen3.5-9B-4bit` | `unsloth/Qwen3.5-9B-GGUF` → `Qwen3.5-9B-Q4_K_M.gguf` |

You never have to choose between MLX and GGUF, or know which one your machine
needs. MyAI picks the artifact that matches the backend it will actually be
loaded by, and shows you the name it settled on.

## Features

- one command for setup, configuration, status, diagnostics and daily use
- an interactive menu, so normal use needs no command-line flags
- model management: install, list, switch, delete, disk usage
- keep-model-in-RAM as a first-class setting, with idle unloading as the
  alternative
- persistent background services through launchd, NSSM or systemd
- OpenCode terminal interface and OpenCode Web, both against the local model
- isolated OpenCode configuration that allows only the local provider
- generated password for remote Web UI access
- configurable context size, output limit, ports and bind addresses
- optional web search and web fetching, off by default
- optional ego lite browser automation, off by default
- real diagnostics, including an actual inference request
- uninstall that keeps your downloaded models unless you say otherwise

## Usage

Open a repository and run MyAI:

```bash
cd ~/source/repos/my-project
myai
```

That opens the menu:

```text
MYAI
local coding agent  ·  darwin/arm64

  1  OpenCode
  2  OpenCode Web
  3  Models
  4  Runtime
  5  Configure
  6  Status
  7  Test
  8  Install / update
  9  Restart services
 10  Uninstall
 11  Quit
```

Choose **OpenCode** to start the terminal interface in the current directory.

Everything in the menu is also a command:

```bash
myai status             # what is installed and running
myai test               # run the built-in checks
myai web                # Web UI address, username and password
myai opencode           # launch OpenCode here
myai models             # list models
myai restart            # restart the background services
```

`myai status` reports the MyAI and OpenCode versions, the inference backend,
the active model and where it stands in memory, the keep-in-RAM setting, the
inference API and service, the OpenCode Web service and address, and the web
search and browser automation settings.

Status is read-only. It never sends a request that would load the model, and
it creates no files, so it is safe to run on a machine where MyAI has not been
installed. That is also why it reports residency from configuration and
service state rather than claiming more precision than it can get for free.

`myai test` checks the real thing rather than the presence of files. It
verifies that the backend is installed, that the service is running, that the
API answers, that the active model is downloaded and actually being served,
that a small inference completes correctly, that OpenCode is installed, that
the managed configuration is still pinned to the local model, and that
OpenCode Web responds to an authenticated request.

## Model management

```text
MYAI · Models

  1  Installed models
  2  Install model
  3  Select active model
  4  Delete model
  5  Disk usage
  6  Back
```

Or from the command line:

```bash
myai models list
myai models install qwen3.5-9b
myai models select qwen3.5-9b-compact
myai models delete unsloth/Qwen3.5-9B-GGUF/Qwen3.5-9B-Q6_K.gguf
myai models usage
```

A model that is already downloaded is never fetched again, and MyAI checks
free disk space before starting a download. Interrupted downloads resume.

Deleting the model that is currently active has to be confirmed explicitly,
because it leaves nothing for the backend to serve.

You can also give a model reference directly, for models outside the catalog:
`org/repo` for MLX, and `org/repo:QUANT` or `org/repo/file.gguf` for GGUF. A
quantization label such as `:Q5_K_M` is looked up in the repository and
resolved to the matching file.

Models are stored in the place the backend expects:

| Platform | Location |
|---|---|
| macOS | `~/.mlx-serve/models`, shared with mlx-serve |
| Linux | `~/.local/share/myai/models` |
| Windows | `%LOCALAPPDATA%\MyAI\data\models` |

## Platform support

| Platform | Backend | Services | Notes |
|---|---|---|---|
| macOS Apple Silicon | mlx-serve, Metal | launchd LaunchAgents | mlx-serve comes from Homebrew |
| Windows x64 | llama.cpp, Vulkan or CUDA or CPU | NSSM | fully native, no WSL |
| Windows ARM64 | llama.cpp, CPU or CUDA | NSSM | NSSM itself is x64 and runs under emulation |
| Linux x64 | llama.cpp, Vulkan or CPU | systemd user units | |
| Linux ARM64 | llama.cpp, CPU | systemd user units | |

MyAI installs the official prebuilt llama.cpp and OpenCode binaries for your
platform. On x64 machines it picks a Vulkan build by default, checks that it
actually runs, and falls back to the portable CPU build if it does not.

Intel Macs are not supported: MLX needs Apple Silicon.

Each platform picks its backend automatically. You can override that under
**Runtime**, including running llama.cpp on Apple Silicon instead of MLX; MyAI
then resolves the active model to the GGUF artifact rather than the MLX one.

## Installation

Every release ships a single binary with no runtime dependencies. Pick the one
for your machine from the
[releases page](https://github.com/Carlboms-Data-AB/myai/releases), or use the
commands below.

**macOS, Apple Silicon**

```bash
curl -fsSL -o myai https://github.com/Carlboms-Data-AB/myai/releases/latest/download/myai-darwin-arm64
chmod +x myai
xattr -d com.apple.quarantine myai 2>/dev/null
./myai status
```

The `xattr` line clears the quarantine flag macOS puts on anything downloaded
with a browser or curl. Without it, Gatekeeper refuses to run an unsigned
binary.

**Linux**

```bash
curl -fsSL -o myai https://github.com/Carlboms-Data-AB/myai/releases/latest/download/myai-linux-amd64
chmod +x myai
./myai status
```

Use `myai-linux-arm64` on ARM machines.

**Windows**, in PowerShell:

```powershell
curl.exe -fsSL -o myai.exe https://github.com/Carlboms-Data-AB/myai/releases/latest/download/myai-windows-amd64.exe
.\myai.exe status
```

Use `myai-windows-arm64.exe` on ARM machines. Installing the services needs an
elevated prompt, because that is what registering a Windows service requires.

Running `status` first is worth the ten seconds. It changes nothing, writes no
files and tells you what the machine already has, so you can see what an
install is about to do.

Then:

```bash
./myai install
```

To build from source instead, which needs Go 1.23 or newer:

```bash
git clone https://github.com/Carlboms-Data-AB/myai.git
cd myai
make build
./myai install
```

`myai install` installs the inference backend, OpenCode, the active model, the
background services and the `myai` command itself, and it puts the command on
your PATH. It is idempotent: anything already present is left alone.

To update later without touching models:

```bash
myai upgrade
```

Upgrading from the earlier `local-ai` Bash version happens automatically the
first time you install. MyAI imports the model, ports, limits and tool settings, keeps the
existing Web UI password so saved logins still work, stops the old
LaunchAgents so they do not hold the ports, and leaves every downloaded model
exactly where it is.

## Configuration and services

Configuration lives in one file:

| Platform | Path |
|---|---|
| macOS and Linux | `~/.config/myai/config.toml` |
| Windows | `%APPDATA%\MyAI\config.toml` |

```toml
active_model = "qwen3.5-9b"
backend = "auto"

[inference]
host = "127.0.0.1"
port = 11234
context = 131072
output = 16384
keep_in_ram = true
idle_unload_minutes = 0

[runtime]
acceleration = "auto"

[web]
enabled = true
host = "0.0.0.0"
port = 4096
username = "opencode"

[tools]
web_search = false
browser_automation = false
```

The **Runtime** and **Configure** menus cover everything you would normally
change; `inference.host` and `web.username` are file-only. A change is
written, the OpenCode configuration is regenerated and the services restart,
in that order.

### Keeping the model in RAM

`keep_in_ram = true` is the default. The model is warmed when the backend
starts and stays resident, so the first request of the day is as fast as the
rest.

Turning it off enables `idle_unload_minutes`. What that does depends on the
backend, and MyAI is explicit about the difference:

- **mlx-serve** unloads idle models natively, through `--idle-evict-secs`.
- **llama.cpp** puts the server to sleep through `--sleep-idle-seconds`. The
  endpoint stays up and the model reloads on the next request. Older
  llama.cpp builds do not have this option; when that is the case MyAI says so
  in `myai status` instead of pretending the setting works.

MyAI never stops the inference service on idle, because that would take the
API away from a running OpenCode session.

### Background services

Two services are managed:

| Platform | Inference | OpenCode Web |
|---|---|---|
| macOS | `se.carlbomsdata.myai` | `se.carlbomsdata.myai-opencode` |
| Windows | `MyAI` | `MyAI-OpenCode` |
| Linux | `myai.service` | `myai-opencode.service` |

On macOS and Linux they run as your own user and start when you log in. On
Windows, NSSM registers them as native services; MyAI sets them to run as your
account rather than LocalSystem, which is why installing them asks for your
Windows password and needs an elevated prompt. The password goes to the
service manager and is never stored by MyAI.

Logs are in `~/.local/state/myai/logs` on macOS and Linux, and in
`%LOCALAPPDATA%\MyAI\state\logs` on Windows.

### OpenCode configuration

MyAI writes its own OpenCode configuration and points OpenCode at it with
`OPENCODE_CONFIG` and `OPENCODE_CONFIG_CONTENT`. Your personal
`~/.config/opencode/opencode.json` is never touched.

Both variables are set deliberately. OpenCode merges configuration from
several places, and setting the inline copy as well means a repository's own
`opencode.json` cannot override the provider and send your work to a cloud
model.

### OpenCode Web

The Web UI binds to `0.0.0.0:4096` by default so it can be reached over a LAN
or an overlay network. `myai web` prints the address, preferring an overlay
address in `100.64.0.0/10` when it finds one:

```text
OpenCode Web

  URL                    http://100.x.x.x:4096
  username               opencode
  password               <generated>
  state                  running
```

Authentication is OpenCode's own, through `OPENCODE_SERVER_USERNAME` and
`OPENCODE_SERVER_PASSWORD`. This is the official OpenCode Web interface; MyAI
adds no web frontend of its own.

### Web search and browser automation

Web search is off by default. Turning it on in **Configure** sets
`OPENCODE_ENABLE_EXA=1` and allows the `websearch` and `webfetch` tools.
Inference stays local either way, but searches then go to Exa and fetches
reach the site being requested.

Browser automation is off by default and installs the `ego-browser` skill when
you enable it. It needs Node.js, and ego lite additionally needs its own macOS
application and a one-time setup in that application. It is opt-in because it
drives a real browser with real signed-in sessions.

## Security

- the inference API binds to `127.0.0.1`, and nothing in the interface changes
  that
- OpenCode Web refuses to start without a password when it is bound beyond
  loopback
- the Web UI password is generated with 18 bytes of cryptographic randomness
- credentials are stored only in the local configuration directory, mode `600`
  on macOS and Linux and restricted to your account on Windows
- credentials are never written into a launch agent, a systemd unit or the
  Windows service registry: the web service runs `myai serve-web`, which reads
  them from the protected file itself
- the managed OpenCode configuration allows exactly one provider, the local one
- Windows services run as your account, not LocalSystem
- MyAI does not modify SSH, GPG, Git remotes or Git configuration

## Uninstall

```text
MYAI · Uninstall

  1  Uninstall MyAI
     Keep downloaded models
  2  Uninstall MyAI and delete models
  3  Delete downloaded models only
  4  Cancel
```

Or:

```bash
myai uninstall                  # removes MyAI, keeps models
myai uninstall --with-models    # removes MyAI and the models
myai uninstall --models-only    # removes only the models
```

The default keeps your models. MyAI shows exactly what it will remove and what
it will keep before it does anything, and deleting models requires typing a
confirmation phrase, because they are large and slow to replace.

Uninstalling removes the services, the configuration, the credentials, the
logs, the tools MyAI downloaded, the PATH entry it added and the `myai`
command. It leaves anything you installed yourself, such as a Homebrew
mlx-serve or an OpenCode already on your PATH.

## Files

| Path | Role |
|---|---|
| `cmd/myai` | command entry point |
| `internal/app` | the core: every operation, with no user interface |
| `internal/cli`, `internal/ui` | the terminal interface |
| `internal/backend` | mlx-serve and llama.cpp adapters |
| `internal/service` | launchd, NSSM and systemd adapters |
| `internal/catalog`, `internal/models` | logical models and the artifacts on disk |
| `internal/opencode` | OpenCode configuration, launching and the Web service |

The core has no user interface of its own. `internal/app` returns structured
results and reports progress through an interface, so the terminal interface is
one caller among possible others.
