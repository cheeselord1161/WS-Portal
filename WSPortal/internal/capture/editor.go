package capture

import (
	"encoding/json"
	"encoding/xml"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// editorLayout selects how an editor's user directory is read.
type editorLayout int

const (
	// layoutVSCode reads <dir>/workspaceStorage/*/workspace.json.
	layoutVSCode editorLayout = iota
	// layoutJetBrains reads <dir>/<Product>/options/recentProjects.xml.
	layoutJetBrains
)

// editorRoot is one editor user directory to scan.
type editorRoot struct {
	// App is the logical application name (ignored for JetBrains, which maps
	// the product directory to an application name).
	App string
	// Dir is the directory to scan.
	Dir string
	// Layout selects the on-disk format.
	Layout editorLayout
}

// EditorSource discovers open editor workspaces by reading the editors' own
// state files. It never inspects command lines or window titles.
type EditorSource struct {
	Roots []editorRoot
}

// Name implements Source.
func (EditorSource) Name() string { return "editors" }

// NewEditorSource builds an editor source for the given home directory, using
// the editor locations that are conventional on the current OS. It returns nil
// when no home directory is known.
func NewEditorSource(home string) Source {
	if strings.TrimSpace(home) == "" {
		return nil
	}

	var roots []editorRoot
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		roots = append(roots,
			editorRoot{"vscode", filepath.Join(base, "Code", "User"), layoutVSCode},
			editorRoot{"vscodium", filepath.Join(base, "VSCodium", "User"), layoutVSCode},
			editorRoot{"cursor", filepath.Join(base, "Cursor", "User"), layoutVSCode},
			editorRoot{"", filepath.Join(base, "JetBrains"), layoutJetBrains},
		)
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		roots = append(roots,
			editorRoot{"vscode", filepath.Join(base, "Code", "User"), layoutVSCode},
			editorRoot{"vscodium", filepath.Join(base, "VSCodium", "User"), layoutVSCode},
			editorRoot{"cursor", filepath.Join(base, "Cursor", "User"), layoutVSCode},
			editorRoot{"", filepath.Join(base, "JetBrains"), layoutJetBrains},
		)
	default:
		base := filepath.Join(home, ".config")
		roots = append(roots,
			editorRoot{"vscode", filepath.Join(base, "Code", "User"), layoutVSCode},
			editorRoot{"vscode", filepath.Join(base, "Code - OSS", "User"), layoutVSCode},
			editorRoot{"vscodium", filepath.Join(base, "VSCodium", "User"), layoutVSCode},
			editorRoot{"cursor", filepath.Join(base, "Cursor", "User"), layoutVSCode},
			editorRoot{"", filepath.Join(base, "JetBrains"), layoutJetBrains},
		)
	}
	return EditorSource{Roots: roots}
}

// Observe implements Source.
func (s EditorSource) Observe() ([]Observation, error) {
	found := map[string][]string{}
	for _, root := range s.Roots {
		switch root.Layout {
		case layoutVSCode:
			if root.App == "" {
				continue
			}
			found[root.App] = appendUnique(found[root.App], readVSCodeWorkspaces(root.Dir)...)
		case layoutJetBrains:
			for product, paths := range readJetBrainsProjects(root.Dir) {
				app := jetBrainsApp(product)
				found[app] = appendUnique(found[app], paths...)
			}
		}
	}

	var observations []Observation
	for _, app := range sortedPathKeys(found) {
		observations = append(observations, Observation{
			Kind:   KindApplication,
			Name:   app,
			Values: found[app],
		})
	}
	return observations, nil
}

// readVSCodeWorkspaces reads the open folders and workspaces recorded under
// <userDir>/workspaceStorage.
func readVSCodeWorkspaces(userDir string) []string {
	storage := filepath.Join(userDir, "workspaceStorage")
	entries, err := os.ReadDir(storage)
	if err != nil {
		return nil
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(storage, entry.Name(), "workspace.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Folder    string `json:"folder"`
			Workspace string `json:"workspace"`
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		for _, raw := range []string{meta.Folder, meta.Workspace} {
			if p := fileURIToPath(raw); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return uniqueSorted(paths)
}

// readJetBrainsProjects reads recent project paths from every product's
// recentProjects.xml under the JetBrains config root.
func readJetBrainsProjects(configRoot string) map[string][]string {
	out := map[string][]string{}
	entries, err := os.ReadDir(configRoot)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(configRoot, entry.Name(), "options", "recentProjects.xml")
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if paths := parseJetBrainsRecent(data); len(paths) > 0 {
			out[entry.Name()] = paths
		}
	}
	return out
}

// parseJetBrainsRecent extracts project paths from recentProjects.xml. It
// supports both the `<map><entry key=...>` and `<list><option value=...>`
// layouts used across JetBrains versions.
func parseJetBrainsRecent(data []byte) []string {
	var doc struct {
		Entries []struct {
			Key string `xml:"key,attr"`
		} `xml:"component>option>map>entry"`
		ListValues []struct {
			Value string `xml:"value,attr"`
		} `xml:"component>option>list>option"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	var paths []string
	for _, e := range doc.Entries {
		if p := strings.TrimSpace(e.Key); p != "" {
			paths = append(paths, p)
		}
	}
	for _, v := range doc.ListValues {
		if p := strings.TrimSpace(v.Value); p != "" {
			paths = append(paths, p)
		}
	}
	return uniqueSorted(paths)
}

// jetBrainsApps maps a JetBrains product directory prefix to a logical name.
var jetBrainsApps = []struct{ prefix, app string }{
	{"IntelliJIdea", "intellij"},
	{"GoLand", "goland"},
	{"PyCharm", "pycharm"},
	{"WebStorm", "webstorm"},
	{"PhpStorm", "phpstorm"},
	{"RubyMine", "rubymine"},
	{"CLion", "clion"},
	{"Rider", "rider"},
	{"DataGrip", "datagrip"},
	{"RustRover", "rustrover"},
	{"AppCode", "appcode"},
	{"Fleet", "fleet"},
}

// jetBrainsApp resolves the logical application name for a product directory.
func jetBrainsApp(product string) string {
	for _, m := range jetBrainsApps {
		if strings.HasPrefix(product, m.prefix) {
			return m.app
		}
	}
	return "intellij"
}

// fileURIToPath converts a file:// URI (as stored by VS Code) to a local path.
func fileURIToPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return ""
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.Clean(p)
}

// appendUnique appends the values in extra that are not already in list.
func appendUnique(list []string, extra ...string) []string {
	for _, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" || contains(list, value) {
			continue
		}
		list = append(list, value)
	}
	return list
}

// uniqueSorted returns the distinct, non-empty, sorted values.
func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// sortedPathKeys returns the keys of a map[string][]string in sorted order.
func sortedPathKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
