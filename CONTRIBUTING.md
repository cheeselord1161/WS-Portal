# Contributing to WSPortal

Thanks for helping make working environments portable.

## Getting set up

Requires Go 1.24 or newer.

```bash
go build -o ws ./cmd/ws
go test ./...
```

## Before opening a pull request

Run all three:

```bash
go build ./...
go test ./...
go vet ./...
```

Or simply `make check`.

## Guidelines

- **Keep packages small.** Each package in `internal/` should have one clear
  responsibility.
- **Keep OS-specific code isolated.** The core must not call platform-specific
  commands directly. Add capabilities to the interfaces in `internal/platform`
  and implement them in the per-OS adapter packages.
- **Cross-platform by default.** Avoid hard-coding assumptions about one
  operating system, path separator, or shell.
- **Test behavior you change.** Prefer table-driven tests and injected
  dependencies over globals.
- **Write useful errors.** Say what went wrong, where, and how to fix it.
- **Never encourage secrets in `.ws` files.** A workspace describes an
  environment; it must not contain credentials.
- **Treat workspaces as untrusted input.** Anything that executes a command
  from a workspace must confirm with the user first.

## Workspace format changes

The `.ws` format is versioned. When you change it:

1. Bump `workspace.CurrentVersion`.
2. Add a migration step in `internal/workspace/version.go` if older files
   should keep working.
3. Update validation for any new required or constrained fields.
4. Update the example in `README.md`.
