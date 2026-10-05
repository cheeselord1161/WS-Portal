# Changelog

All notable changes to WSPortal are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 0.1.1

### Fixed

* Fixed an issue where captured applications were not properly launched during workspace restoration.
* Improved application launch handling during `ws resume`.
* Improved restoration of supported applications such as VS Code and terminal applications.
* Added safer handling for applications that are unavailable on the current system.

### Platform Support

* Official releases are currently available for Linux and Windows.
* macOS support remains under development and is not included in official releases until it can be properly tested on real macOS hardware.


## [0.1.0] - 2026-10-04

The first public release. WSPortal captures a working environment as a
declarative `.ws` file and rebuilds it on another machine.

### Added

- `.ws` workspace format: strict YAML parsing, round-trip load/save, and
  portable variables such as `${WORKSPACE_ROOT}` and `${HOME}`.
- Schema versioning with migrations. Version 2 replaced the flat browser tab
  list with window-grouped, titled tabs; version 1 files are read and upgraded
  on load.
- Commands: `init`, `capture`, `inspect`, `validate`, `resume`, `export`,
  `import`, `send`, `receive`, `list`, and `version`.
- `ws resume` builds a restore plan; `resume --execute` applies it, launching
  applications and browsers, opening terminals, cloning the project, and
  starting services. Commands taken from a workspace are treated as untrusted
  and confirmed first.
- `resume --install-deps` installs declared missing tools with the platform
  package manager, after per-package confirmation.
- Automatic capture (`ws capture`): running applications and services, terminal
  directories, installed tools, open editor workspaces (VS Code and JetBrains),
  and open browser tabs (Chromium family and Firefox, including Firefox tab
  titles and window grouping). Re-capturing merges into an existing workspace;
  `--overwrite` replaces it.
- LAN transfer (`ws send` / `ws receive`) with UDP discovery and a TCP handoff.
- Platform adapters for Linux and Windows behind small capability
  interfaces in `internal/platform`.

### Known limitations

- Remote transfer across networks (temporary codes) is not implemented and is
  not exposed in the CLI yet.
- Workspace history and smart workflow discovery are planned but not
  implemented.

[Unreleased]: https://github.com/cheeselord1161/WS_Portal/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/cheeselord1161/WS_Portal/releases/tag/v0.1.0
