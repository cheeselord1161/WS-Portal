// Package workspace defines the in-memory representation of a .ws workspace
// file, along with loading, saving, and versioning helpers.
//
// A workspace is a declarative description of the environment a user *wants*
// to work in, not a recording of what they did. It is designed to be
// human-readable, portable across operating systems, and safe to share.
package workspace

// CurrentVersion is the schema version written by this build of WSPortal.
// Bump it whenever the on-disk schema changes in a way that older builds
// cannot understand. See version.go for migration hooks.
//
// Version 2 replaced the flat browser tab list (tabs: [urls]) with
// window-grouped, titled tabs (windows: [{tabs: [{url, title}]}]).
const CurrentVersion = 2

// Workspace is the root document of a .ws file.
type Workspace struct {
	// Version is the schema version of this document. It is required so that
	// future builds can migrate or reject documents safely.
	Version int `yaml:"version"`

	// Workspace holds identity metadata (name, description).
	Workspace Identity `yaml:"workspace"`

	// Project describes the source code the environment is built around.
	Project *Project `yaml:"project,omitempty"`

	// Applications are GUI/editor applications to open.
	Applications []Application `yaml:"applications,omitempty"`

	// Browser describes browser sessions and the tabs to open.
	Browser []BrowserSession `yaml:"browser,omitempty"`

	// Terminals describes shells to open, optionally running a command.
	Terminals []Terminal `yaml:"terminals,omitempty"`

	// Services are long-running dependencies (databases, containers, ...).
	Services []Service `yaml:"services,omitempty"`

	// Environment describes runtimes and tools the environment expects.
	Environment Environment `yaml:"environment,omitempty"`

	// Metadata holds provenance information about how the file was produced.
	Metadata Metadata `yaml:"metadata,omitempty"`
}

// Identity is the "workspace:" block: who this workspace is.
type Identity struct {
	// Name is the short, human-friendly identifier for the workspace.
	Name string `yaml:"name"`
	// Description is an optional one-line summary.
	Description string `yaml:"description,omitempty"`
}

// Project describes where the code lives and how to obtain it.
type Project struct {
	// Name is the project name, which may differ from the workspace name.
	Name string `yaml:"name"`
	// Source describes how to obtain the code. It is optional for projects
	// that already exist locally.
	Source *Source `yaml:"source,omitempty"`
	// Path is the local path, normally expressed with portable variables such
	// as ${WORKSPACE_ROOT}. It may be empty when the path is unknown.
	Path string `yaml:"path,omitempty"`
}

// Source describes how to obtain a project's code.
type Source struct {
	// Type is the source kind, for example "git" or "local".
	Type string `yaml:"type"`
	// URL is the remote location for the source.
	URL string `yaml:"url,omitempty"`
	// Branch is the branch, tag, or revision to check out.
	Branch string `yaml:"branch,omitempty"`
}

// Application is a desktop application to launch as part of the environment.
type Application struct {
	// ID is a stable identifier used to refer to this application.
	ID string `yaml:"id"`
	// Name is the application name understood by the platform adapter,
	// for example "vscode" or "terminal".
	Name string `yaml:"name"`
	// Open lists files or folders to open in the application.
	Open []string `yaml:"open,omitempty"`
	// WorkingDirectory is the directory the application should start in.
	WorkingDirectory string `yaml:"working_directory,omitempty"`
}

// BrowserSession describes one browser instance and the tabs it should open.
type BrowserSession struct {
	// Browser is the browser name, for example "chrome" or "firefox".
	Browser string `yaml:"browser"`
	// Profile optionally selects a browser profile.
	Profile string `yaml:"profile,omitempty"`
	// Windows groups the tabs by the browser window they were open in. A
	// browser that only exposes a flat list of tabs uses a single window.
	Windows []BrowserWindow `yaml:"windows,omitempty"`
	// Tabs is the pre-v2 flat tab list. It is retained so older files still
	// decode, and is migrated into Windows on load. It is never written back.
	//
	// Deprecated: use Windows.
	Tabs []string `yaml:"tabs,omitempty"`
}

// BrowserTab is one tab to open.
type BrowserTab struct {
	// URL is the page address.
	URL string `yaml:"url"`
	// Title is the page title, when it is known. It is informational and does
	// not affect restore.
	Title string `yaml:"title,omitempty"`
}

// BrowserWindow groups the tabs that were open in one browser window.
type BrowserWindow struct {
	// Tabs are the tabs to open in this window.
	Tabs []BrowserTab `yaml:"tabs,omitempty"`
}

// migrateLegacyTabs folds a pre-v2 flat tab list into a single window. It is
// idempotent: once Tabs is empty it does nothing. A session that already has
// windows keeps them and appends the legacy tabs as one more window.
func (b *BrowserSession) migrateLegacyTabs() {
	if b == nil || len(b.Tabs) == 0 {
		return
	}
	tabs := make([]BrowserTab, 0, len(b.Tabs))
	for _, u := range b.Tabs {
		if u == "" {
			continue
		}
		tabs = append(tabs, BrowserTab{URL: u})
	}
	b.Tabs = nil
	if len(tabs) > 0 {
		b.Windows = append(b.Windows, BrowserWindow{Tabs: tabs})
	}
}

// TabURLs returns every tab URL in window order, then any legacy flat tabs.
func (b BrowserSession) TabURLs() []string {
	var out []string
	for _, w := range b.Windows {
		for _, t := range w.Tabs {
			if t.URL != "" {
				out = append(out, t.URL)
			}
		}
	}
	for _, u := range b.Tabs {
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

// Terminal describes a shell to open, optionally running a command.
type Terminal struct {
	// Name is a label for the terminal, for example "server".
	Name string `yaml:"name,omitempty"`
	// WorkingDirectory is the directory the shell should start in.
	WorkingDirectory string `yaml:"working_directory,omitempty"`
	// Command is an optional command to run. Commands from imported
	// workspaces must be treated as untrusted and confirmed with the user.
	Command string `yaml:"command,omitempty"`
}

// Service is an external dependency such as a database or container engine.
type Service struct {
	// Name is the service name, for example "docker" or "postgres".
	Name string `yaml:"name"`
	// Version optionally pins a required version.
	Version string `yaml:"version,omitempty"`
	// Required marks the service as mandatory for the workspace.
	Required bool `yaml:"required,omitempty"`
}

// Environment describes runtimes and tools the workspace expects.
type Environment struct {
	// Runtime maps a runtime name to its version, for example node: "22".
	Runtime map[string]string `yaml:"runtime,omitempty"`
	// Tools lists command-line tools expected to be available.
	Tools []string `yaml:"tools,omitempty"`
}

// Metadata holds provenance information about the workspace file.
type Metadata struct {
	// CreatedBy identifies the tool that produced the file.
	CreatedBy string `yaml:"created_by,omitempty"`
	// CreatedAt is an RFC3339 timestamp of when the file was created.
	CreatedAt string `yaml:"created_at,omitempty"`
}

// Name returns the workspace's name, falling back to the project name.
// It never returns an empty string when either name is set.
func (w *Workspace) Name() string {
	if w == nil {
		return ""
	}
	if w.Workspace.Name != "" {
		return w.Workspace.Name
	}
	if w.Project != nil {
		return w.Project.Name
	}
	return ""
}
