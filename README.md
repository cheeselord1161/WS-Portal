# Workspace Portal

> **Workspace Portal — portable workspaces, anywhere.**

WSPortal makes your working environment portable. Describe the environment you
work in once, then reconstruct it on another computer with a single command.

> **Status: early development.** The CLI runs and restoration, capture, and
> local-network transfer work on Linux and Windows, but remote transfer
> is not implemented yet.

## Installation

### Prebuilt binaries

Download the archive for your platform from the
[releases page](https://github.com/cheeselord1161/WS-Portal/releases). Each
release ships:

| Platform | Architectures |
| -------- | ------------- |
| Linux    | amd64, arm64  |
| Windows  | amd64, arm64  |

Archives are `tar.gz` on Linux and `zip` on Windows, and every
release includes a `checksums.txt`. Unpack the archive and put `ws` (or
`ws.exe`) somewhere on your `PATH`.

### With Go

If you have Go 1.24 or newer:

```bash
go install github.com/cheeselord1161/WS-Portal/cmd/ws@latest
```

This installs `ws` to `$(go env GOPATH)/bin`.

### From source

```bash
git clone https://github.com/cheeselord1161/WS-Portal.git
cd WS-Portal
make build          # or: go build -o ws ./cmd/ws
```

## What problem does it solve?

Moving to another computer is painful. Your project is easy to copy, but the
environment around it is not: which applications you had open, which browser
tabs you were using, which terminals were running which commands, which
services were up, which runtime versions were installed. You end up rebuilding
all of that by hand, every time.

WSPortal captures that environment as a small, readable file and rebuilds it
elsewhere.

## The basic concept

WSPortal does **not** back up your files, and it is **not** a macro recorder.
It does not record mouse movements, keystrokes, or screen activity.

Instead it stores a *declarative description* of the environment you want:

- the project and where it comes from
- the applications to open, and what to open in them
- the browser tabs to restore
- the terminals to open, and the commands to run in them
- the services and tools that must be available
- the runtimes and versions the project expects

You save this once, move to another machine, and restore it.

The core idea:

> **WSPortal describes your working environment as portable state rather than
> recording your computer as an immutable snapshot.**

## Example `.ws` file

A workspace is a versioned YAML document. Paths use portable variables such as
`${WORKSPACE_ROOT}` so the same file works on any machine.

```yaml
version: 2

workspace:
  name: myproject
  description: MyProject development environment

project:
  name: myproject
  source:
    type: git
    url: https://github.com/user/myproject.git
    branch: main
  path: ${WORKSPACE_ROOT}/myproject

applications:
  - id: editor
    name: vscode
    open:
      - ${WORKSPACE_ROOT}/myproject

  - id: terminal
    name: terminal
    working_directory: ${WORKSPACE_ROOT}/myproject

browser:
  - browser: chrome
    windows:
      - tabs:
          - url: https://github.com/user/myproject
          - url: http://localhost:3000
          - url: https://docs.example.com

terminals:
  - name: server
    working_directory: ${WORKSPACE_ROOT}/myproject
    command: npm run dev

services:
  - name: docker
    required: true

  - name: postgres
    version: "16"
    required: true

environment:
  runtime:
    node: "22"

  tools:
    - git
    - docker

metadata:
  created_by: wsportal
```

### Portable paths

Machine-specific paths defeat the purpose of a portable workspace. Instead of:

```yaml
path: /home/alice/projects/myproject
```

write:

```yaml
path: ${WORKSPACE_ROOT}/myproject
```

Each machine defines `WORKSPACE_ROOT` for itself:

| Platform | `WORKSPACE_ROOT`          |
| -------- | ------------------------- |
| Linux    | `/home/user/projects`     |
| Windows  | `D:\Projects`             |

WSPortal resolves these variables at restore time. Set `WORKSPACE_ROOT` in your
environment to override the default (which is `~/projects`).

## Command-line interface

The executable is `ws`. The command set:

```text
ws
├── init        Create a new workspace definition
├── capture     Discover the current environment
├── inspect     Display a workspace file
├── validate    Check a workspace file
├── resume      Restore an environment (or preview the plan)
├── export      Copy a workspace file somewhere shareable
├── import      Import a workspace file
├── send        Send a workspace to another machine (LAN)
├── receive     Receive a workspace from another machine (LAN)
├── list        List locally available workspaces
├── version     Print version information
├── doctor      Check the local environment for restore dependencies
└── completion  Generate shell completion scripts
```

Bash, zsh, fish, and PowerShell completion are available via
`ws completion <shell>`.

### Quick start

```bash
ws init myproject
ws inspect myproject
ws validate myproject
ws resume myproject
ws resume myproject --execute
ws list
ws doctor
```

`ws doctor` reports which tools and applications this machine has for restoring a workspace — Git, VS Code, Chrome, and so on. Use it before `ws resume` to see what is missing.

## Transfer modes

There are 2 ways to move a workspace between machines:

1. **`.ws` file** — `ws export` / `ws import`. A plain YAML file you can email,
   commit, or copy to a USB stick. No account, no network required.
2. **Local network** — `ws send myproject` / `ws receive`. Transfer directly
   between machines on the same LAN; the sender announces itself and the
   receiver finds it automatically. Implemented.


## Security philosophy

A workspace file can contain commands, URLs, paths, and service information, so
it must be treated as untrusted input.

- **Never blindly execute commands.** Commands from a workspace are flagged
  during validation and marked as requiring confirmation before they run.
- **No secrets.** Do not put passwords, API keys, tokens, private keys,
  authentication cookies, or browser session credentials in a `.ws` file. A
  workspace describes an environment; it must not contain credentials.
- **Review before sharing.** The file is plain text and easy to read. Read it.
- **Trust your network.** LAN transfer sends the workspace in the clear over
  your local network. A workspace contains no secrets by design, but only send
  on a network you trust.



## Architecture

```text
cmd/ws/               Executable entrypoint
internal/cli/         Cobra commands and their dependencies
internal/workspace/   Data model, load/save, versioning, variable resolution
internal/config/      Workspace directory and per-machine variables
internal/validation/  Structural validation and issue reporting
internal/restore/     Restore planner, executor, cloning, and dependency installer
internal/capture/     Capture engine: processes, editor workspaces, browser tabs
internal/transfer/    Transfer interfaces, LAN sender/receiver, placeholders
internal/platform/    OS capability interfaces and per-OS adapters
  ├── linux/
  ├── windows/
  └── detect/         Selects the adapter for the running OS
```

The core never shells out to a platform-specific command directly. It depends
on the small interfaces in `internal/platform` (app, browser, and terminal
launchers, plus a service manager and process lister), which per-OS adapters
implement. Cross-platform tools such as `git` are the exception.

```text
             Core WSPortal
                   │
      ┌────────────┼────────────┐
      │                         │
   Windows                    Linux
   adapter                   adapter
```

## Development

Requires Go 1.24 or newer.

```bash
go build ./...
go test ./...
go vet ./...
```

`make check` runs the same checks CI runs (formatting, vet, `golangci-lint`,
and the test suite); it needs [golangci-lint v2](https://golangci-lint.run/welcome/install/)
on your `PATH`.

### Releasing

Releases are built by GoReleaser in the
[release workflow](.github/workflows/release.yml). To publish a version:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Pushing the tag builds `ws` for Linux and Windows (amd64 and arm64),
attaches the archives and a `checksums.txt` to a **draft** GitHub release, and
injects the version into `ws version`. Review the draft, then publish it. The
configuration lives in [`.goreleaser.yml`](.goreleaser.yml); run
`goreleaser check` to validate it locally.

### Contributing

Contributions are welcome. To get started:

1. Open an issue describing the change, or pick up an existing one.
2. Keep packages small and focused, and keep OS-specific code behind the
   interfaces in `internal/platform`.
3. Add tests for behavior you change. Cross-platform behavior matters — avoid
   hard-coding assumptions about one operating system.
4. Run `go build ./...`, `go test ./...`, and `go vet ./...` before opening a
   pull request.
5. Keep the workspace format human-readable and safe to share. Never add a
   field that encourages storing secrets.

## License

MIT. See [LICENSE](LICENSE).
