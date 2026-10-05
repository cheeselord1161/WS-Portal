package platform

import (
	"path/filepath"
	"strings"
)

// KnownApplication describes a logical desktop application that WSPortal can
// capture and restore.
//
// The ID is stable across operating systems. It is what a workspace records,
// so the same .ws file can be restored anywhere; each OS adapter maps the ID to
// a local executable. Aliases let executable names, display names, and older
// logical names resolve to the same ID.
type KnownApplication struct {
	// ID is the canonical, portable identifier, e.g. "vscode".
	ID string
	// Name is the human-friendly name shown in the restore plan, e.g. "VS Code".
	Name string
	// Aliases are alternative references that resolve to this application:
	// executable names, common display names, and legacy logical names.
	Aliases []string
}

// knownApplications is the small, portable catalogue of applications WSPortal
// understands. It is deliberately not exhaustive — WSPortal prioritises a
// correct, safe foundation over a large database. Each OS adapter maps an ID to
// its own executable or bundle, so extending support is a matter of adding an
// entry here and, when needed, a per-OS executable alias.
var knownApplications = []KnownApplication{
	{ID: "vscode", Name: "VS Code", Aliases: []string{"code", "code-oss", "visual studio code", "vs code"}},
	{ID: "vscodium", Name: "VSCodium", Aliases: []string{"codium"}},
	{ID: "cursor", Name: "Cursor"},
	{ID: "intellij", Name: "IntelliJ IDEA", Aliases: []string{"idea", "idea64", "intellij idea"}},
	{ID: "goland", Name: "GoLand"},
	{ID: "pycharm", Name: "PyCharm"},
	{ID: "webstorm", Name: "WebStorm"},
	{ID: "phpstorm", Name: "PhpStorm"},
	{ID: "rubymine", Name: "RubyMine"},
	{ID: "clion", Name: "CLion"},
	{ID: "rider", Name: "Rider"},
	{ID: "datagrip", Name: "DataGrip"},
	{ID: "rustrover", Name: "RustRover"},
	{ID: "appcode", Name: "AppCode"},
	{ID: "fleet", Name: "Fleet"},
	{ID: "sublime", Name: "Sublime Text", Aliases: []string{"subl", "sublime_text", "sublime text"}},
	{ID: "neovim", Name: "Neovim", Aliases: []string{"nvim"}},
	{ID: "vim", Name: "Vim", Aliases: []string{"gvim"}},
	{ID: "emacs", Name: "Emacs"},
	{ID: "gimp", Name: "GIMP"},
	{ID: "inkscape", Name: "Inkscape"},
	{ID: "blender", Name: "Blender"},
	{ID: "postman", Name: "Postman"},
	{ID: "dbeaver", Name: "DBeaver"},
	{ID: "slack", Name: "Slack"},
	{ID: "spotify", Name: "Spotify"},
	{ID: "notion", Name: "Notion"},
	{ID: "docker", Name: "Docker Desktop", Aliases: []string{"docker desktop"}},
	{ID: "notepad", Name: "Notepad"},
	{ID: "explorer", Name: "File Explorer", Aliases: []string{"file explorer", "windows explorer"}},
	{ID: "chrome", Name: "Google Chrome", Aliases: []string{"google-chrome", "google-chrome-stable", "google chrome"}},
	{ID: "chromium", Name: "Chromium", Aliases: []string{"chromium-browser"}},
	{ID: "firefox", Name: "Firefox", Aliases: []string{"firefox-bin", "mozilla firefox"}},
	{ID: "edge", Name: "Microsoft Edge", Aliases: []string{"msedge", "microsoft-edge", "microsoft edge"}},
	{ID: "brave", Name: "Brave", Aliases: []string{"brave-browser", "brave browser"}},
	{ID: "vivaldi", Name: "Vivaldi"},
	{ID: "opera", Name: "Opera"},
	{ID: "terminal", Name: "Terminal", Aliases: []string{"wt", "windows terminal", "gnome-terminal", "konsole", "xterm", "alacritty", "kitty", "wezterm", "xfce4-terminal"}},
}

// normalizeAppName lowercases a name and strips any directory and .exe suffix,
// so an executable name ("Code.exe"), a display name ("Visual Studio Code"),
// and a logical id ("vscode") all compare predictably.
func normalizeAppName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSuffix(name, ".exe")
	return strings.ToLower(strings.TrimSpace(name))
}

// LookupApplication resolves a logical id, alias, executable name, or display
// name to a catalogued application. It reports whether the reference is known.
func LookupApplication(name string) (KnownApplication, bool) {
	key := normalizeAppName(name)
	if key == "" {
		return KnownApplication{}, false
	}
	for _, app := range knownApplications {
		if normalizeAppName(app.ID) == key {
			return app, true
		}
		for _, alias := range app.Aliases {
			if normalizeAppName(alias) == key {
				return app, true
			}
		}
	}
	return KnownApplication{}, false
}

// CanonicalAppID returns the portable identifier for a name. A known id or
// alias is normalized to the catalogue id; an unknown name is returned in
// normalized form so it stays stable but is not treated as a launchable
// application.
func CanonicalAppID(name string) string {
	if app, ok := LookupApplication(name); ok {
		return app.ID
	}
	return normalizeAppName(name)
}

// KnownApplications returns the catalogued applications in listing order. The
// caller receives a copy and may not modify the catalogue.
func KnownApplications() []KnownApplication {
	out := make([]KnownApplication, len(knownApplications))
	copy(out, knownApplications)
	return out
}
