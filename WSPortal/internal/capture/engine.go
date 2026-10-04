package capture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// defaultCaptureTools are the command-line tools the engine probes for when
// building the workspace's environment section.
var defaultCaptureTools = []string{
	"git", "go", "node", "npm", "pnpm", "yarn", "python3", "docker",
	"make", "cargo", "rustc", "java", "kubectl", "terraform",
}

// Limits keep a capture from recording an unbounded number of entries.
const (
	maxOpenPathsPerApp = 10
	maxTabsPerBrowser  = 25
)

// ProcessEngine is a real capture Engine. It gathers observations from one or
// more sources and builds a workspace that describes the running environment.
//
// It is deliberately conservative: it never records command lines, keystrokes,
// or screen contents. Editor workspaces and browser tabs are read from the
// applications' own state files, not from the screen.
type ProcessEngine struct {
	// Sources are the observation sources consulted.
	Sources []Source
	// Tools are the tool names probed against LookPath.
	Tools []string
	// LookPath reports a tool's path and whether it was found.
	LookPath func(string) (string, bool)
	// HomeDir, when set, is rewritten to ${HOME} in captured paths and used to
	// locate editor and browser state.
	HomeDir string
}

// NewProcessEngine builds an engine backed by the adapter's process lister,
// plus editor-workspace and browser-tab sources derived from HomeDir.
func NewProcessEngine(p *platform.Adapter) *ProcessEngine {
	e := &ProcessEngine{
		Tools: defaultCaptureTools,
		LookPath: func(name string) (string, bool) {
			path, err := exec.LookPath(name)
			if err != nil {
				return "", false
			}
			return path, true
		},
	}
	if home, err := os.UserHomeDir(); err == nil {
		e.HomeDir = home
	}
	if p != nil && p.Processes != nil && p.Processes.Supported() {
		e.Sources = append(e.Sources, ProcessSource{Processes: p.Processes, HomeDir: e.HomeDir})
	}
	if s := NewEditorSource(e.HomeDir); s != nil {
		e.Sources = append(e.Sources, s)
	}
	if s := NewBrowserSource(e.HomeDir); s != nil {
		e.Sources = append(e.Sources, s)
	}
	return e
}

// DisableSources removes sources by name, e.g. "browsers". It lets the CLI
// offer opt-outs for the more sensitive sources.
func (e *ProcessEngine) DisableSources(names ...string) {
	drop := make(map[string]bool, len(names))
	for _, name := range names {
		drop[name] = true
	}
	kept := e.Sources[:0]
	for _, source := range e.Sources {
		if drop[source.Name()] {
			continue
		}
		kept = append(kept, source)
	}
	e.Sources = kept
}

// Capture implements Engine.
func (e *ProcessEngine) Capture(name string) (*workspace.Workspace, error) {
	if e == nil || len(e.Sources) == 0 {
		return nil, ErrNotImplemented
	}

	var observations []Observation
	succeeded := 0
	var firstErr error
	var failedSource string
	for _, source := range e.Sources {
		got, err := source.Observe()
		if err != nil {
			// Capture is best-effort: one failing source should not abort the
			// others. But if every source fails, the caller must hear about it
			// rather than receiving an empty workspace.
			if firstErr == nil {
				firstErr = err
				failedSource = source.Name()
			}
			continue
		}
		succeeded++
		observations = append(observations, got...)
	}
	if succeeded == 0 && firstErr != nil {
		return nil, fmt.Errorf("capture %s: %w", failedSource, firstErr)
	}

	return e.build(name, observations), nil
}

// build turns observations into a workspace, merging repeated observations of
// the same application or browser.
func (e *ProcessEngine) build(name string, observations []Observation) *workspace.Workspace {
	ws := workspace.New(name, "Captured from the running environment")
	ws.Metadata.CreatedBy = "wsportal (capture)"

	apps := map[string]*workspace.Application{}
	browsers := map[string]*workspace.BrowserSession{}
	var terminals []workspace.Terminal
	seenTerminal := map[string]bool{}
	var services []workspace.Service
	seenService := map[string]bool{}

	for _, o := range observations {
		switch o.Kind {
		case KindProject:
			if len(o.Values) == 0 {
				continue
			}
			ws.Project = &workspace.Project{
				Name: o.Name,
				Path: e.portablePath(o.Values[0]),
			}
		case KindApplication:
			if o.Name == "" {
				continue
			}
			app, ok := apps[o.Name]
			if !ok {
				app = &workspace.Application{ID: slug(o.Name), Name: o.Name}
				apps[o.Name] = app
			}
			for _, value := range o.Values {
				dir := e.portablePath(value)
				if dir == "" || contains(app.Open, dir) {
					continue
				}
				if len(app.Open) >= maxOpenPathsPerApp {
					break
				}
				app.Open = append(app.Open, dir)
			}
			if app.WorkingDirectory == "" && len(app.Open) > 0 {
				app.WorkingDirectory = app.Open[0]
			}
		case KindBrowser:
			if o.Name == "" {
				continue
			}
			session, ok := browsers[o.Name]
			if !ok {
				session = &workspace.BrowserSession{Browser: o.Name}
				browsers[o.Name] = session
			}
			appendBrowserTabs(session, o.Tabs)
		case KindTerminal:
			if len(o.Values) == 0 {
				continue
			}
			dir := e.portablePath(o.Values[0])
			if dir == "" || seenTerminal[dir] {
				continue
			}
			seenTerminal[dir] = true
			label := o.Name
			if label == "" {
				label = "terminal"
			}
			terminals = append(terminals, workspace.Terminal{
				Name:             label,
				WorkingDirectory: dir,
			})
		case KindService:
			if o.Name == "" || seenService[o.Name] {
				continue
			}
			seenService[o.Name] = true
			services = append(services, workspace.Service{Name: o.Name})
		}
	}

	ws.Applications = sortedApplications(apps)
	ws.Browser = sortedBrowsers(browsers)
	ws.Terminals = terminals
	ws.Services = services
	ws.Environment.Tools = e.detectTools()
	return ws
}

// appendBrowserTabs adds an observation's tabs to a session, grouped by the
// window they were seen in and capped at maxTabsPerBrowser overall. Duplicate
// URLs within a window are dropped, and an already-present title is kept only
// if the incoming tab has none.
func appendBrowserTabs(session *workspace.BrowserSession, tabs []TabObservation) {
	total := 0
	for _, window := range session.Windows {
		total += len(window.Tabs)
	}

	var order []int
	grouped := map[int][]workspace.BrowserTab{}
	for _, tab := range tabs {
		url := strings.TrimSpace(tab.URL)
		if url == "" {
			continue
		}
		if total >= maxTabsPerBrowser {
			break
		}
		if _, seen := grouped[tab.Window]; !seen {
			order = append(order, tab.Window)
		}
		if windowHasURL(grouped[tab.Window], url) {
			continue
		}
		grouped[tab.Window] = append(grouped[tab.Window], workspace.BrowserTab{
			URL:   url,
			Title: strings.TrimSpace(tab.Title),
		})
		total++
	}

	sort.Ints(order)
	for _, window := range order {
		session.Windows = append(session.Windows, workspace.BrowserWindow{Tabs: grouped[window]})
	}
}

// windowHasURL reports whether a window already holds a tab for url.
func windowHasURL(tabs []workspace.BrowserTab, url string) bool {
	for _, tab := range tabs {
		if tab.URL == url {
			return true
		}
	}
	return false
}

// detectTools returns the sorted subset of the probed tools that is installed.
func (e *ProcessEngine) detectTools() []string {
	if e.LookPath == nil {
		return nil
	}
	var found []string
	for _, tool := range e.Tools {
		if _, ok := e.LookPath(tool); ok {
			found = append(found, tool)
		}
	}
	sort.Strings(found)
	return found
}

// portablePath rewrites a path under the user's home to use ${HOME}.
func (e *ProcessEngine) portablePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	home := filepath.Clean(e.HomeDir)
	if e.HomeDir == "" || home == "." {
		return p
	}
	p = filepath.Clean(p)
	if p == home {
		return "${HOME}"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "${HOME}" + p[len(home):]
	}
	return p
}

// sortedApplications returns the accumulated applications sorted by name.
func sortedApplications(m map[string]*workspace.Application) []workspace.Application {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]workspace.Application, 0, len(names))
	for _, name := range names {
		out = append(out, *m[name])
	}
	return out
}

// sortedBrowsers returns the accumulated browser sessions sorted by name.
func sortedBrowsers(m map[string]*workspace.BrowserSession) []workspace.BrowserSession {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]workspace.BrowserSession, 0, len(names))
	for _, name := range names {
		out = append(out, *m[name])
	}
	return out
}

// contains reports whether a string slice already holds value.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// slug turns a display name into a stable identifier.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "app"
	}
	return out
}
