// Package capture observes the user's current working environment and turns it
// into a workspace description.
//
// WSPortal is not a macro recorder. Capture is intended to recognize
// meaningful workflow context — which project is open, which applications are
// running, which local URLs are in play — never keystrokes, mouse movement, or
// screen contents.
//
// The interfaces below define the shape of the engine. The default
// implementation is a real process-inspection engine.
package capture

import (
	"fmt"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// Observation is a single meaningful fact about the environment.
//
// Each observation is a small, typed, declarative statement ("VS Code has this
// folder open") rather than a raw event.
type Observation struct {
	// Kind categorizes the observation, e.g. "application", "browser",
	// "terminal", "service".
	Kind string
	// Name identifies the subject, e.g. "vscode".
	Name string
	// Values are the associated values, e.g. an open folder or a URL.
	Values []string
	// Tabs carries browser tabs with the extra detail some sources can
	// recover: the page title and the window the tab belongs to.
	Tabs []TabObservation
}

// TabObservation is one browser tab seen by a source.
type TabObservation struct {
	// URL is the tab's current address.
	URL string
	// Title is the page title, when the source can recover it.
	Title string
	// Window identifies the browser window the tab belongs to. Tabs sharing a
	// Window are recorded together; 0 is a valid window.
	Window int
}

// Source discovers observations from some part of the environment.
//
// Implementations might watch running processes, open windows, shell history,
// or running containers. Keeping this an interface means new sources can be
// added without changing the engine.
type Source interface {
	// Name identifies the source, e.g. "processes".
	Name() string
	// Observe returns the observations this source can currently see.
	Observe() ([]Observation, error)
}

// Engine turns observations from one or more sources into a workspace.
type Engine interface {
	// Capture collects observations and builds a workspace named name.
	Capture(name string) (*workspace.Workspace, error)
}

// TODO(capture): add workflow discovery that notices repeated patterns and
// suggests creating a workspace.

// ErrNotImplemented is returned by engines that cannot capture on this machine.
var ErrNotImplemented = fmt.Errorf("automatic capture is not implemented yet")


