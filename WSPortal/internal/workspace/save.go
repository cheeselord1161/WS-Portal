package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// header is prepended to every written workspace file to explain what it is.
const header = "# WSPortal workspace file\n" +
	"# This file describes a portable working environment.\n" +
	"# It is safe to read, edit, version-control, and share.\n" +
	"# Do not store secrets (passwords, tokens, keys) in this file.\n"

// Encode serializes a workspace to YAML, including the explanatory header.
func Encode(ws *Workspace) ([]byte, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	var buf bytes.Buffer
	buf.WriteString(header)

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(ws); err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}

	return buf.Bytes(), nil
}

// Save writes a workspace to path.
//
// When path does not end in .ws the extension is appended. The file is written
// atomically (write to a temp file, then rename) so an interrupted save never
// leaves a half-written workspace behind.
func Save(ws *Workspace, path string) error {
	if path == "" {
		return fmt.Errorf("workspace path is empty")
	}
	if !HasExtension(path) {
		path += Extension
	}

	data, err := Encode(ws)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create workspace directory: %w", err)
		}
	}

	tmp, err := os.CreateTemp(dir, ".ws-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp workspace file: %w", err)
	}
	tmpName := tmp.Name()

	// Best-effort cleanup if anything below fails.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write workspace: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close workspace: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}

	return nil
}
