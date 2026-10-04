package capture

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
)

// Observation kinds produced by the built-in sources.
const (
	// KindApplication is a running desktop application.
	KindApplication = "application"
	// KindBrowser is a running browser.
	KindBrowser = "browser"
	// KindTerminal is a shell with a working directory.
	KindTerminal = "terminal"
	// KindService is a long-running service process.
	KindService = "service"
	// KindProject is a detected project root.
	KindProject = "project"
)

// appProcesses maps a process name to the logical application name WSPortal
// uses in a workspace.
var appProcesses = map[string]string{
	"code":         "vscode",
	"code-oss":     "vscode",
	"codium":       "vscodium",
	"idea":         "intellij",
	"goland":       "goland",
	"pycharm":      "pycharm",
	"webstorm":     "webstorm",
	"rustrover":    "rustrover",
	"subl":         "sublime",
	"sublime_text": "sublime",
	"atom":         "atom",
	"nvim":         "neovim",
	"vim":          "vim",
	"emacs":        "emacs",
	"gimp":         "gimp",
	"inkscape":     "inkscape",
	"blender":      "blender",
	"postman":      "postman",
	"dbeaver":      "dbeaver",
	"slack":        "slack",
	"spotify":      "spotify",
}

// browserProcesses maps a process name to a browser name.
var browserProcesses = map[string]string{
	"chrome":               "chrome",
	"google-chrome":        "chrome",
	"google-chrome-stable": "chrome",
	"chromium":             "chromium",
	"chromium-browser":     "chromium",
	"firefox":              "firefox",
	"firefox-bin":          "firefox",
	"brave":                "brave",
	"brave-browser":        "brave",
	"msedge":               "edge",
	"microsoft-edge":       "edge",
	"opera":                "opera",
	"vivaldi":              "vivaldi",
	"safari":               "safari",
}

// terminalProcesses lists interactive shells.
var terminalProcesses = map[string]bool{
	"bash": true, "zsh": true, "fish": true, "sh": true, "dash": true,
	"nu": true, "pwsh": true, "powershell": true, "cmd": true, "ksh": true,
}

// serviceProcesses maps a process name to a service name.
var serviceProcesses = map[string]string{
	"dockerd":      "docker",
	"postgres":     "postgres",
	"postmaster":   "postgres",
	"redis-server": "redis",
	"mysqld":       "mysql",
	"mongod":       "mongodb",
	"nginx":        "nginx",
	"caddy":        "caddy",
}

// projectMarkers are files/directories that identify a project root.
var projectMarkers = []string{
	".git", "go.mod", "package.json", "Cargo.toml", "pyproject.toml",
	"pom.xml", "build.gradle", "Gemfile", "composer.json",
}

// ProcessSource discovers running applications, browsers, terminals, services,
// and the project they are working on by inspecting the process list.
type ProcessSource struct {
	// Processes lists running processes. A nil lister yields no observations.
	Processes platform.ProcessLister
	// HomeDir, when set, is treated as an ambient directory: it is never
	// reported as a project root, and no terminal is captured there.
	HomeDir string
}

// Name implements Source.
func (ProcessSource) Name() string { return "processes" }

// Observe implements Source. It returns a stable, sorted set of observations.
func (s ProcessSource) Observe() ([]Observation, error) {
	if s.Processes == nil {
		return nil, nil
	}
	procs, err := s.Processes.List()
	if err != nil {
		return nil, err
	}

	apps := map[string]string{}
	browsers := map[string]bool{}
	terminals := map[string]bool{}
	services := map[string]bool{}
	var cwds []string

	for _, p := range procs {
		name := procName(p)
		cwd := strings.TrimSpace(p.Cwd)
		if cwd != "" && !s.isNoiseDir(cwd) {
			cwds = append(cwds, cwd)
		}

		switch {
		case appProcesses[name] != "":
			logical := appProcesses[name]
			dir := ""
			if cwd != "" && !s.isNoiseDir(cwd) {
				dir = cwd
			}
			if cur, ok := apps[logical]; !ok || cur == "" {
				apps[logical] = dir
			}
		case browserProcesses[name] != "":
			browsers[browserProcesses[name]] = true
		case terminalProcesses[name]:
			if cwd != "" && !s.isNoiseDir(cwd) {
				terminals[cwd] = true
			}
		case serviceProcesses[name] != "":
			services[serviceProcesses[name]] = true
		}
	}

	var observations []Observation

	if root := s.dominantProject(cwds); root != "" {
		observations = append(observations, Observation{
			Kind:   KindProject,
			Name:   filepath.Base(root),
			Values: []string{root},
		})
	}

	for _, name := range sortedStringKeys(apps) {
		o := Observation{Kind: KindApplication, Name: name}
		if dir := apps[name]; dir != "" {
			o.Values = []string{dir}
		}
		observations = append(observations, o)
	}
	for _, name := range sortedBoolKeys(browsers) {
		observations = append(observations, Observation{Kind: KindBrowser, Name: name})
	}
	for _, dir := range sortedBoolKeys(terminals) {
		observations = append(observations, Observation{Kind: KindTerminal, Name: "terminal", Values: []string{dir}})
	}
	for _, name := range sortedBoolKeys(services) {
		observations = append(observations, Observation{Kind: KindService, Name: name})
	}

	return observations, nil
}

// isNoiseDir reports whether a process working directory is ambient OS state
// rather than a place a user is working. Capturing these produces useless
// entries (home directories, caches, /proc, ...).
func (s ProcessSource) isNoiseDir(dir string) bool {
	dir = filepath.Clean(dir)
	if dir == "." || dir == string(filepath.Separator) {
		return true
	}
	if s.HomeDir != "" && dir == filepath.Clean(s.HomeDir) {
		return true
	}
	for _, prefix := range noisePrefixes {
		if dir == prefix || strings.HasPrefix(dir, prefix+string(filepath.Separator)) {
			return true
		}
	}
	if s.HomeDir != "" {
		home := filepath.Clean(s.HomeDir)
		for _, rel := range homeNoiseDirs {
			noisy := filepath.Join(home, rel)
			if dir == noisy || strings.HasPrefix(dir, noisy+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// noisePrefixes are absolute directories that never describe user work. /tmp
// is intentionally absent: scratch projects are legitimate, if unusual.
var noisePrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/var", "/etc", "/usr", "/boot",
}

// homeNoiseDirs are directories under the user's home that hold caches and
// application state rather than projects.
var homeNoiseDirs = []string{
	".cache", ".config", ".local", ".var", ".steam", ".mozilla",
	".thunderbird", ".gnupg", ".ssh", "Library",
}

// dominantProject picks the most likely project root among the process working
// directories.
//
// It prefers the most specific candidate: a project nested inside another
// candidate is where the user is actually working, while a parent directory
// (such as a home directory that happens to contain a stray marker) merely
// collects ambient processes. Among candidates that are unrelated, the one with
// the most processes wins, with a lexicographic tie-break for determinism.
func (s ProcessSource) dominantProject(cwds []string) string {
	counts := map[string]int{}
	for _, dir := range cwds {
		root := findProjectRoot(dir)
		if root == "" || s.isHome(root) {
			continue
		}
		counts[root]++
	}
	if len(counts) == 0 {
		return ""
	}

	roots := sortedIntKeys(counts)
	var kept []string
	for _, root := range roots {
		if isAncestorOfAny(root, roots) {
			continue
		}
		kept = append(kept, root)
	}
	if len(kept) == 0 {
		return ""
	}

	best := kept[0]
	for _, root := range kept[1:] {
		if counts[root] > counts[best] || (counts[root] == counts[best] && root < best) {
			best = root
		}
	}
	return best
}

// isHome reports whether dir is the user's home directory.
func (s ProcessSource) isHome(dir string) bool {
	return s.HomeDir != "" && filepath.Clean(dir) == filepath.Clean(s.HomeDir)
}

// isAncestorOfAny reports whether parent is a strict ancestor of another
// candidate root.
func isAncestorOfAny(parent string, roots []string) bool {
	prefix := parent + string(filepath.Separator)
	for _, other := range roots {
		if other != parent && strings.HasPrefix(other, prefix) {
			return true
		}
	}
	return false
}

// procName returns the normalized executable name for a process.
func procName(p platform.Process) string {
	name := p.Name
	if name == "" && len(p.Args) > 0 {
		name = p.Args[0]
	}
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimSuffix(name, ".exe")
	return filepath.Base(name)
}

// findProjectRoot walks up from dir looking for a project marker. It never
// returns a filesystem root.
func findProjectRoot(dir string) string {
	dir = filepath.Clean(dir)
	root := string(filepath.Separator)
	for i := 0; i < 6; i++ {
		if dir == root {
			return ""
		}
		for _, marker := range projectMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
