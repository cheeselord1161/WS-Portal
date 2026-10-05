// Package validation checks that a workspace is structurally sound before it
// is inspected, shared, or restored.
//
// Validation is deliberately separate from loading: loading answers "can this
// be parsed?", validation answers "does this describe a usable environment?".
package validation

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// Severity classifies how serious a validation issue is.
type Severity string

const (
	// SeverityError means the workspace cannot be used as-is.
	SeverityError Severity = "error"
	// SeverityWarning means the workspace is usable but suspicious.
	SeverityWarning Severity = "warning"
)

// Issue is a single validation finding tied to a location in the document.
type Issue struct {
	// Field is the dotted path to the offending field, e.g. "project.name".
	Field string
	// Message explains what is wrong and, where possible, how to fix it.
	Message string
	// Severity classifies the issue.
	Severity Severity
}

// String renders an issue as "field: message".
func (i Issue) String() string {
	if i.Field == "" {
		return i.Message
	}
	return fmt.Sprintf("%s: %s", i.Field, i.Message)
}

// Result is the outcome of validating a workspace.
type Result struct {
	// Issues is every finding, in a stable order.
	Issues []Issue
}

// OK reports whether validation found no errors. Warnings do not fail
// validation.
func (r Result) OK() bool {
	for _, i := range r.Issues {
		if i.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Errors returns only the error-severity issues.
func (r Result) Errors() []Issue {
	return r.filter(SeverityError)
}

// Warnings returns only the warning-severity issues.
func (r Result) Warnings() []Issue {
	return r.filter(SeverityWarning)
}

func (r Result) filter(s Severity) []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Severity == s {
			out = append(out, i)
		}
	}
	return out
}

// Validator validates workspaces. It is a struct (rather than a bare function)
// so that future rules — supported application names, service catalogues,
// policy checks — can be injected without changing call sites.
type Validator struct {
	// KnownApplications optionally restricts application names. When empty, any
	// non-empty name is accepted.
	KnownApplications []string
	// KnownBrowsers optionally restricts browser names. When empty, any
	// non-empty name is accepted.
	KnownBrowsers []string
}

// New returns a Validator with default rules.
func New() *Validator {
	return &Validator{}
}

// Validate checks the workspace and returns all findings.
func (v *Validator) Validate(ws *workspace.Workspace) Result {
	var issues []Issue
	add := func(field, msg string, sev Severity) {
		issues = append(issues, Issue{Field: field, Message: msg, Severity: sev})
	}

	if ws == nil {
		add("", "workspace is nil", SeverityError)
		return Result{Issues: issues}
	}

	// --- Schema version -----------------------------------------------------
	if ws.Version <= 0 {
		add("version", fmt.Sprintf("missing schema version (expected %d)", workspace.CurrentVersion), SeverityError)
	} else if ws.Version > workspace.CurrentVersion {
		add("version", fmt.Sprintf(
			"schema version %d is newer than this build supports (max %d)",
			ws.Version, workspace.CurrentVersion), SeverityError)
	}

	// --- Identity -----------------------------------------------------------
	if strings.TrimSpace(ws.Workspace.Name) == "" {
		add("workspace.name", "name is required", SeverityError)
	}

	// --- Project ------------------------------------------------------------
	if ws.Project != nil {
		if strings.TrimSpace(ws.Project.Name) == "" {
			add("project.name", "name is required when project is set", SeverityError)
		}
		if src := ws.Project.Source; src != nil {
			if strings.TrimSpace(src.Type) == "" {
				add("project.source.type", "source type is required", SeverityError)
			}
			if strings.EqualFold(src.Type, "git") {
				if strings.TrimSpace(src.URL) == "" {
					add("project.source.url", "git sources require a URL", SeverityError)
				} else if !isValidRemote(src.URL) {
					add("project.source.url", fmt.Sprintf("invalid git URL %q", src.URL), SeverityError)
				}
			}
		}
	}

	// --- Applications -------------------------------------------------------
	seenAppID := map[string]bool{}
	for i, app := range ws.Applications {
		base := fmt.Sprintf("applications[%d]", i)
		if strings.TrimSpace(app.ID) == "" {
			add(base+".id", "id is required", SeverityError)
		} else if seenAppID[app.ID] {
			add(base+".id", fmt.Sprintf("duplicate application id %q", app.ID), SeverityError)
		} else {
			seenAppID[app.ID] = true
		}
		// The id is the portable identity; name is optional. The resolved
		// reference (name when set, otherwise id) is checked against the
		// trusted catalogue, or the injected allow-list, so an unknown
		// application is visible without being fatal.
		logical := app.LogicalName()
		if strings.TrimSpace(logical) == "" {
			add(base+".id", "an application id or name is required", SeverityError)
		} else if !v.applicationAllowed(logical) {
			add(base+".name", fmt.Sprintf("unknown application %q", logical), SeverityWarning)
		}
	}

	// --- Browser ------------------------------------------------------------
	for i, b := range ws.Browser {
		base := fmt.Sprintf("browser[%d]", i)
		if strings.TrimSpace(b.Browser) == "" {
			add(base+".browser", "browser name is required", SeverityError)
		} else if !v.browserAllowed(b.Browser) {
			add(base+".browser", fmt.Sprintf("unknown browser %q", b.Browser), SeverityWarning)
		}
		for j, tab := range b.TabURLs() {
			if strings.TrimSpace(tab) == "" {
				add(fmt.Sprintf("%s.tabs[%d]", base, j), "empty tab URL", SeverityWarning)
				continue
			}
			if !isValidURLOrPath(tab) {
				add(fmt.Sprintf("%s.tabs[%d]", base, j),
					fmt.Sprintf("invalid URL %q", tab), SeverityWarning)
			}
		}
	}

	// --- Terminals ----------------------------------------------------------
	for i, t := range ws.Terminals {
		base := fmt.Sprintf("terminals[%d]", i)
		if t.Command != "" {
			// Commands from workspaces are untrusted input. We do not reject
			// them, but we make the risk visible.
			add(base+".command",
				fmt.Sprintf("runs a command and will require confirmation before execution: %q", t.Command),
				SeverityWarning)
		}
	}

	// --- Services -----------------------------------------------------------
	seenService := map[string]bool{}
	for i, s := range ws.Services {
		base := fmt.Sprintf("services[%d]", i)
		if strings.TrimSpace(s.Name) == "" {
			add(base+".name", "service name is required", SeverityError)
		} else if seenService[s.Name] {
			add(base+".name", fmt.Sprintf("duplicate service %q", s.Name), SeverityWarning)
		} else {
			seenService[s.Name] = true
		}
	}

	// --- Environment --------------------------------------------------------
	for name, version := range ws.Environment.Runtime {
		if strings.TrimSpace(name) == "" {
			add("environment.runtime", "runtime name cannot be empty", SeverityError)
		}
		if strings.TrimSpace(version) == "" {
			add("environment.runtime."+name, "runtime version cannot be empty", SeverityWarning)
		}
	}
	for i, tool := range ws.Environment.Tools {
		if strings.TrimSpace(tool) == "" {
			add(fmt.Sprintf("environment.tools[%d]", i), "tool name cannot be empty", SeverityWarning)
		}
	}

	// --- Portable variables -------------------------------------------------
	// Referencing an undefined variable is not an error at validation time
	// (another machine may define it), so this is informational only and is
	// surfaced by `ws inspect` / `ws resume` instead.

	// Sort for deterministic output, then stable-sort errors before warnings.
	sort.SliceStable(issues, func(a, b int) bool {
		if issues[a].Field != issues[b].Field {
			return issues[a].Field < issues[b].Field
		}
		return issues[a].Message < issues[b].Message
	})

	return Result{Issues: issues}
}

func (v *Validator) applicationAllowed(name string) bool {
	if len(v.KnownApplications) > 0 {
		for _, a := range v.KnownApplications {
			if strings.EqualFold(a, name) {
				return true
			}
		}
		return false
	}
	// No explicit allow-list: accept anything the portable catalogue knows.
	_, ok := platform.LookupApplication(name)
	return ok
}

func (v *Validator) browserAllowed(name string) bool {
	if len(v.KnownBrowsers) == 0 {
		return true
	}
	for _, b := range v.KnownBrowsers {
		if strings.EqualFold(b, name) {
			return true
		}
	}
	return false
}

// isValidRemote reports whether s looks like a usable git remote. It accepts
// URLs (https, ssh, git) and scp-style remotes such as git@host:path.
func isValidRemote(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "git@") || strings.Contains(s, "@") && strings.Contains(s, ":") {
		return true
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https", "ssh", "git", "file":
		return u.Host != "" || u.Scheme == "file"
	default:
		return false
	}
}

// isValidURLOrPath accepts http(s) URLs as well as localhost URLs with ports
// and simple absolute paths.
func isValidURLOrPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme == "" {
		// Treat as a filesystem path.
		return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "${")
	}
	switch u.Scheme {
	case "http", "https":
		return u.Host != ""
	default:
		return false
	}
}
