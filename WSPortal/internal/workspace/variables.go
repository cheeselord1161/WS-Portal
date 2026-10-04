package workspace

import (
	"fmt"
	"regexp"
	"strings"
)

// varPattern matches ${NAME} style references, optionally followed by
// :-fallback. The name group and the optional default group are both
// capturing so callers can index spans by position.
var varPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// Well-known portable variables. Values are resolved per machine, which is
// what makes a single .ws file usable on Windows, macOS, and Linux.
const (
	// VarWorkspaceRoot is the directory under which projects live.
	VarWorkspaceRoot = "WORKSPACE_ROOT"
	// VarHome is the user's home directory.
	VarHome = "HOME"
)

// Resolver resolves variable names to machine-specific values.
//
// The interface is intentionally tiny so that callers can inject anything from
// an environment map to a test fixture.
type Resolver interface {
	// Lookup returns the value for name and whether it was found.
	Lookup(name string) (string, bool)

	// Defaults returns the fallback map for ${VAR:-x} substitutions.
	Defaults() map[string]string
}

// MapResolver is a Resolver backed by a plain map plus per-map fallback map
// for ${VAR:-x} default substitutions.
type MapResolver struct {
	// variables is the primary variable store. Embedding it lets callers
	// index a MapResolver with m[name] as before.
	variables map[string]string

	// fallbacks maps a variable name to a fallback value that Resolve
	// substitutes in ${VAR:-x} references when no real value is set.
	fallbacks map[string]string
}

// NewMapResolver builds a MapResolver from overrides and optional defaults.
func NewMapResolver(overrides map[string]string, defaults ...map[string]string) *MapResolver {
	m := &MapResolver{
		variables: make(map[string]string),
		fallbacks: make(map[string]string),
	}
	for k, v := range overrides {
		m.variables[k] = v
	}
	for _, d := range defaults {
		for k, v := range d {
			m.fallbacks[k] = v
		}
	}
	return m
}

// Default records a default value for a variable.
func (m *MapResolver) Default(name, def string) {
	if m.fallbacks == nil {
		m.fallbacks = make(map[string]string)
	}
	m.fallbacks[name] = def
}

// Lookup implements Resolver.
func (m MapResolver) Lookup(name string) (string, bool) {
	if v, ok := m.variables[name]; ok {
		return v, true
	}
	if v, ok := m.fallbacks[name]; ok {
		return v, true
	}
	return "", false
}

// Defaults implements Resolver.
func (m MapResolver) Defaults() map[string]string {
	return m.fallbacks
}

// Resolve expands ${VAR} references in s using r.
//
// It returns an error naming the first undefined variable, because a silently
// empty path would produce a confusing restore failure later.
//
// Semantics:
//
//   - ${VAR} with no default: replaced by r.Lookup's value, or an error if
//     the variable is unknown.
//   - ${VAR:-default} with no resolver value: the original "${VAR:-default}"
//     text is left untouched (it is shell syntax, not a portable reference).
//   - ${VAR:-default} where r resolves VAR (variable or fallback): the
//     resolver value is substituted, ignoring the literal default.
func Resolve(s string, r Resolver) (string, error) {
	if s == "" {
		return s, nil
	}

	// FindAllStringSubmatchIndex gives byte positions of the whole match and
	// of each capturing group. That is what lets us walk the string without
	// ever slicing away the original text.
	matches := varPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, nil
	}

	// Resolve each referenced variable once, recording unknown ones for the
	// error message.
	value := make(map[string]string, len(matches))
	unknown := map[string]bool{}
	for _, m := range matches {
		name := s[m[2]:m[3]]
		if v, ok := r.Lookup(name); ok {
			value[name] = v
			continue
		}
		def := ""
		if m[4] >= 0 {
			def = s[m[4]:m[5]]
		}
		if def != "" {
			// ${VAR:-default} found with no real value. Keep the literal so
			// shell-style fallbacks are not silently mis-parsed.
			continue
		}
		unknown[name] = true
	}

	var b strings.Builder
	prev := 0
	for _, m := range matches {
		// Text between this span and the previous span.
		b.WriteString(s[prev:m[0]])

		name := s[m[2]:m[3]]
		if val, ok := value[name]; ok {
			b.WriteString(val)
		} else {
			// ${VAR:-default} where VAR had no resolver value: preserve the
			// original text in place of the reference.
			b.WriteString(s[m[0]:m[1]])
		}
		prev = m[1]
	}
	b.WriteString(s[prev:])

	if len(unknown) > 0 {
		for name := range unknown {
			return "", fmt.Errorf("undefined variable(s): %s", name)
		}
	}
	return b.String(), nil
}

// Variables returns the distinct variable names referenced by s, in order of
// first appearance.
func Variables(s string) []string {
	seen := map[string]bool{}
	var names []string
	for _, m := range varPattern.FindAllStringSubmatch(s, -1) {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// RequiredVariables returns the distinct variable names referenced anywhere in
// the workspace. Callers use this to tell the user exactly which variables a
// machine must define before the workspace can be restored.
func (w *Workspace) RequiredVariables() []string {
	seen := map[string]bool{}
	var names []string
	add := func(s string) {
		for _, name := range Variables(s) {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}

	if w.Project != nil {
		add(w.Project.Path)
	}
	for _, app := range w.Applications {
		add(app.WorkingDirectory)
		for _, p := range app.Open {
			add(p)
		}
	}
	for _, t := range w.Terminals {
		add(t.WorkingDirectory)
	}
	for _, b := range w.Browser {
		for _, tab := range b.TabURLs() {
			add(tab)
		}
	}

	return names
}

// ResolvePaths returns a deep copy of the workspace with every path-bearing
// field expanded through r. The original workspace is never mutated, so the
// portable form is preserved for saving and sharing.
func (w *Workspace) ResolvePaths(r Resolver) (*Workspace, error) {
	if w == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	resolved := w.clone()

	expand := func(s string) (string, error) {
		if s == "" {
			return "", nil
		}
		return Resolve(s, r)
	}

	if resolved.Project != nil {
		p, err := expand(resolved.Project.Path)
		if err != nil {
			return nil, fmt.Errorf("project.path: %w", err)
		}
		resolved.Project.Path = p
	}

	for i := range resolved.Applications {
		app := &resolved.Applications[i]
		wd, err := expand(app.WorkingDirectory)
		if err != nil {
			return nil, fmt.Errorf("applications[%d].working_directory: %w", i, err)
		}
		app.WorkingDirectory = wd
		for j, p := range app.Open {
			v, err := expand(p)
			if err != nil {
				return nil, fmt.Errorf("applications[%d].open[%d]: %w", i, j, err)
			}
			app.Open[j] = v
		}
	}

	for i := range resolved.Terminals {
		wd, err := expand(resolved.Terminals[i].WorkingDirectory)
		if err != nil {
			return nil, fmt.Errorf("terminals[%d].working_directory: %w", i, err)
		}
		resolved.Terminals[i].WorkingDirectory = wd
	}

	for i := range resolved.Browser {
		for j := range resolved.Browser[i].Windows {
			tabs := resolved.Browser[i].Windows[j].Tabs
			for k := range tabs {
				v, err := expand(tabs[k].URL)
				if err != nil {
					return nil, fmt.Errorf("browser[%d].windows[%d].tabs[%d].url: %w", i, j, k, err)
				}
				tabs[k].URL = v
			}
		}
		for j, tab := range resolved.Browser[i].Tabs {
			v, err := expand(tab)
			if err != nil {
				return nil, fmt.Errorf("browser[%d].tabs[%d]: %w", i, j, err)
			}
			resolved.Browser[i].Tabs[j] = v
		}
	}

	return resolved, nil
}

// clone returns a deep copy of the workspace.
func (w *Workspace) clone() *Workspace {
	cp := *w

	if w.Project != nil {
		p := *w.Project
		if w.Project.Source != nil {
			s := *w.Project.Source
			p.Source = &s
		}
		cp.Project = &p
	}

	cp.Applications = append([]Application(nil), w.Applications...)
	for i := range cp.Applications {
		cp.Applications[i].Open = append([]string(nil), w.Applications[i].Open...)
	}

	cp.Terminals = append([]Terminal(nil), w.Terminals...)

	cp.Browser = append([]BrowserSession(nil), w.Browser...)
	for i := range cp.Browser {
		cp.Browser[i].Tabs = append([]string(nil), w.Browser[i].Tabs...)
		cp.Browser[i].Windows = append([]BrowserWindow(nil), w.Browser[i].Windows...)
		for j := range cp.Browser[i].Windows {
			cp.Browser[i].Windows[j].Tabs = append([]BrowserTab(nil), w.Browser[i].Windows[j].Tabs...)
		}
	}

	cp.Services = append([]Service(nil), w.Services...)
	cp.Environment.Tools = append([]string(nil), w.Environment.Tools...)
	if w.Environment.Runtime != nil {
		cp.Environment.Runtime = make(map[string]string, len(w.Environment.Runtime))
		for k, v := range w.Environment.Runtime {
			cp.Environment.Runtime[k] = v
		}
	}

	return &cp
}
