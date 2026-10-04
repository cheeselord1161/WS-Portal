package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// resolveWorkspacePath finds the .ws file for a user-supplied argument.
//
// The argument may be a path to a file, a path without the extension, or a
// bare workspace name stored in the configured workspace directory. The first
// match wins, in that order.
func (a *App) resolveWorkspacePath(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", fmt.Errorf("no workspace specified")
	}

	// 1. An explicit path that already exists.
	if fileExists(arg) {
		return arg, nil
	}

	// 2. A path without the extension.
	if !workspace.HasExtension(arg) && fileExists(arg+workspace.Extension) {
		return arg + workspace.Extension, nil
	}

	// 3. A bare name in the configured workspace directory.
	name := strings.TrimSuffix(arg, workspace.Extension)
	if name != arg {
		// The argument had an extension but the file was not found; report the
		// path the user typed rather than searching by name.
		return "", fmt.Errorf("workspace file not found: %s", arg)
	}
	if a.Config != nil && a.Config.WorkspaceDir != "" {
		candidate := filepath.Join(a.Config.WorkspaceDir, name+workspace.Extension)
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("workspace not found: %s", arg)
}

// listWorkspaceFiles returns the .ws files in the configured workspace
// directory, sorted by name. A missing directory yields an empty list rather
// than an error.
func (a *App) listWorkspaceFiles() ([]string, error) {
	if a.Config == nil || a.Config.WorkspaceDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(a.Config.WorkspaceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace directory: %w", err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if workspace.HasExtension(e.Name()) {
			files = append(files, filepath.Join(a.Config.WorkspaceDir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
