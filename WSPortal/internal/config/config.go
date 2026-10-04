// Package config resolves where WSPortal stores workspaces and how portable
// variables such as ${WORKSPACE_ROOT} map to this machine.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// AppName is the directory name used under the user's config/data directory.
const AppName = "wsportal"

// Config holds the resolved runtime configuration for a single invocation.
//
// It is constructed once in the CLI and passed down to commands, which keeps
// global state out of the lower layers and makes them straightforward to test.
type Config struct {
	// WorkspaceDir is the directory where workspaces are stored by default.
	WorkspaceDir string
	// Variables holds portable variable overrides from the environment.
	Variables map[string]string
}

// Load builds a Config using the process environment.
func Load() (*Config, error) {
	dir, err := DefaultWorkspaceDir()
	if err != nil {
		return nil, err
	}
	return &Config{
		WorkspaceDir: dir,
		Variables:    EnvironmentVariables(),
	}, nil
}

// DefaultWorkspaceDir returns the per-user directory for stored workspaces.
//
// It follows the platform conventions: XDG on Linux, Application Support on
// macOS, and AppData on Windows.
func DefaultWorkspaceDir() (string, error) {
	// An explicit override always wins; useful for tests and for users who
	// keep their workspaces on a shared drive.
	if v := strings.TrimSpace(os.Getenv("WSPORTAL_HOME")); v != "" {
		return v, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", errors.New("cannot determine user config directory")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, AppName, "workspaces"), nil
}

// EnvironmentVariables returns the portable variables available on this
// machine. These are the values used to expand ${VAR} references in paths.
func EnvironmentVariables() map[string]string {
	vars := map[string]string{}

	if home, err := os.UserHomeDir(); err == nil {
		vars[workspace.VarHome] = home
	}

	// WORKSPACE_ROOT defaults to the user's home directory, but users are
	// encouraged to set it explicitly (see README).
	if root := strings.TrimSpace(os.Getenv(workspace.VarWorkspaceRoot)); root != "" {
		vars[workspace.VarWorkspaceRoot] = root
	} else if home, ok := vars[workspace.VarHome]; ok {
		vars[workspace.VarWorkspaceRoot] = filepath.Join(home, "projects")
	}

	// Expose other environment variables as a convenience, but only those
	// referenced as portable variables so we do not leak the whole
	// environment into resolution.
	for _, name := range commonPassthrough {
		if v, ok := os.LookupEnv(name); ok {
			if _, exists := vars[name]; !exists {
				vars[name] = v
			}
		}
	}

	return vars
}

// commonPassthrough lists environment variables that are safe and useful to
// expose as portable variables without copying the entire environment.
var commonPassthrough = []string{
	"USER",
	"USERNAME",
	"HOME",
	"USERPROFILE",
	"PWD",
}

// Resolver returns a workspace.Resolver backed by this configuration.
func (c *Config) Resolver() workspace.Resolver {
	return workspace.NewMapResolver(c.Variables)
}

// Platform returns the runtime.GOOS value. It is wrapped so that command code
// does not depend on the runtime package directly and so tests can describe
// behavior per platform.
func (c *Config) Platform() string {
	return runtime.GOOS
}
