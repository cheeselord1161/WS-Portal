package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNotWorkspaceFile is returned when a path does not have the .ws extension.
var ErrNotWorkspaceFile = errors.New("not a .ws workspace file")

// Extension is the canonical file extension for workspace files.
const Extension = ".ws"

// Load reads and decodes a workspace from the given path.
//
// The file must use the .ws extension. Unknown YAML keys are rejected so that
// typos surface as errors instead of being silently ignored.
func Load(path string) (*Workspace, error) {
	if !HasExtension(path) {
		return nil, fmt.Errorf("%w: %s", ErrNotWorkspaceFile, path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	defer f.Close()

	return Decode(f)
}

// Decode decodes a workspace from a reader.
func Decode(r io.Reader) (*Workspace, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read workspace: %w", err)
	}

	var ws Workspace
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&ws); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("workspace file is empty")
		}
		return nil, fmt.Errorf("parse workspace: %w", err)
	}

	if err := ws.normalize(); err != nil {
		return nil, err
	}
	if err := ws.migrate(); err != nil {
		return nil, fmt.Errorf("migrate workspace: %w", err)
	}

	return &ws, nil
}

// HasExtension reports whether path ends with the .ws extension.
func HasExtension(path string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), Extension)
}

// normalize applies in-memory defaults that do not affect the on-disk format.
func (w *Workspace) normalize() error {
	if w.Version == 0 {
		// A missing version is treated as the current version rather than a hard
		// error so that hand-written files remain usable. Validation reports it.
		w.Version = CurrentVersion
	}
	if w.Environment.Runtime == nil {
		w.Environment.Runtime = map[string]string{}
	}
	// Fold any pre-v2 flat tab list into a single window so the rest of the
	// code can rely on the window-grouped representation.
	for i := range w.Browser {
		w.Browser[i].migrateLegacyTabs()
	}
	return nil
}
