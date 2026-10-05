package capture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
)

type fakeLister struct {
	procs []platform.Process
	err   error
}

func (f fakeLister) Supported() bool                   { return true }
func (f fakeLister) List() ([]platform.Process, error) { return f.procs, f.err }

func TestProcessEngineCapturesEnvironment(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "projects", "myproject")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module myproject\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lister := fakeLister{procs: []platform.Process{
		{PID: 1, Name: "code", Cwd: project},
		{PID: 2, Name: "bash", Cwd: project},
		{PID: 3, Name: "chrome"},
		{PID: 4, Name: "dockerd"},
		{PID: 5, Name: "redis-server", Cwd: "/"},
	}}

	engine := &ProcessEngine{
		Sources:  []Source{ProcessSource{Processes: lister}},
		Tools:    []string{"git", "docker", "definitely-not-a-real-tool"},
		LookPath: func(name string) (string, bool) { return "/usr/bin/" + name, name != "definitely-not-a-real-tool" },
		HomeDir:  home,
	}

	ws, err := engine.Capture("captured")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if ws.Project == nil || ws.Project.Name != "myproject" {
		t.Fatalf("Project = %+v, want myproject", ws.Project)
	}
	if want := "${HOME}/projects/myproject"; ws.Project.Path != want {
		t.Errorf("Project.Path = %q, want %q", ws.Project.Path, want)
	}

	if len(ws.Applications) != 1 || ws.Applications[0].ID != "vscode" {
		t.Fatalf("Applications = %+v, want one vscode", ws.Applications)
	}
	if want := "${HOME}/projects/myproject"; ws.Applications[0].Open[0] != want {
		t.Errorf("application open = %q, want %q", ws.Applications[0].Open[0], want)
	}

	if len(ws.Browser) != 1 || ws.Browser[0].Browser != "chrome" {
		t.Errorf("Browser = %+v, want chrome", ws.Browser)
	}
	if len(ws.Terminals) != 1 || ws.Terminals[0].WorkingDirectory != "${HOME}/projects/myproject" {
		t.Errorf("Terminals = %+v", ws.Terminals)
	}

	gotServices := map[string]bool{}
	for _, s := range ws.Services {
		gotServices[s.Name] = true
	}
	if !gotServices["docker"] || !gotServices["redis"] {
		t.Errorf("Services = %+v, want docker and redis", ws.Services)
	}

	if len(ws.Environment.Tools) != 2 || ws.Environment.Tools[0] != "docker" || ws.Environment.Tools[1] != "git" {
		t.Errorf("Tools = %v, want [docker git]", ws.Environment.Tools)
	}
}

func TestProcessSourceDeduplicatesApplicationsAndSkipsHelpers(t *testing.T) {
	lister := fakeLister{procs: []platform.Process{
		{PID: 1, Name: "chrome"},
		{PID: 2, Name: "chrome"},
		{PID: 3, Name: "Google Chrome"},
		{PID: 4, Name: "code", Cwd: "/work/app"},
		{PID: 5, Name: "code-oss", Cwd: "/work/app"},
		{PID: 6, Name: "code Helper (Renderer)"},
		{PID: 7, Name: "chrome_crashpad_handler"},
	}}

	obs, err := ProcessSource{Processes: lister}.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}

	apps := map[string]int{}
	browsers := map[string]int{}
	for _, o := range obs {
		switch o.Kind {
		case KindApplication:
			apps[o.Name]++
		case KindBrowser:
			browsers[o.Name]++
		}
	}
	if len(apps) != 1 || apps["vscode"] != 1 {
		t.Errorf("applications = %v, want exactly one vscode", apps)
	}
	if len(browsers) != 1 || browsers["chrome"] != 1 {
		t.Errorf("browsers = %v, want exactly one chrome", browsers)
	}
	for name := range apps {
		if strings.Contains(name, "helper") {
			t.Errorf("helper process captured as application: %q", name)
		}
	}
}

func TestProcessEngineWithoutSources(t *testing.T) {
	engine := &ProcessEngine{}
	if _, err := engine.Capture("x"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Capture error = %v, want ErrNotImplemented", err)
	}
}

func TestProcessEnginePropagatesSourceError(t *testing.T) {
	engine := &ProcessEngine{Sources: []Source{
		ProcessSource{Processes: fakeLister{err: errors.New("boom")}},
	}}
	_, err := engine.Capture("x")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Capture error = %v, want the source error", err)
	}
}

func TestDominantProjectPrefersSpecificRoot(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "go.mod"), []byte("module parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "services", "api")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "go.mod"), []byte("module child\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The parent has more processes, but it is an ancestor of the child, so the
	// more specific project wins.
	s := ProcessSource{}
	cwds := []string{parent, parent, parent, child}
	if got := s.dominantProject(cwds); got != child {
		t.Errorf("dominantProject = %q, want %q", got, child)
	}
}

func TestDominantProjectIgnoresHome(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "Documents", "WSPortal")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module ws\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := ProcessSource{HomeDir: home}
	cwds := []string{home, home, home, repo}
	if got := s.dominantProject(cwds); got != repo {
		t.Errorf("dominantProject = %q, want %q", got, repo)
	}
}

func TestIsNoiseDir(t *testing.T) {
	home := t.TempDir()
	s := ProcessSource{HomeDir: home}

	noisy := []string{
		string(filepath.Separator),
		home,
		filepath.Join(home, ".cache", "thing"),
		filepath.Join(home, ".local", "share", "Steam"),
	}
	for _, dir := range noisy {
		if !s.isNoiseDir(dir) {
			t.Errorf("isNoiseDir(%q) = false, want true", dir)
		}
	}

	fine := []string{
		filepath.Join(home, "projects", "app"),
		filepath.Join(home, "Documents", "WSPortal"),
		t.TempDir(),
	}
	for _, dir := range fine {
		if s.isNoiseDir(dir) {
			t.Errorf("isNoiseDir(%q) = true, want false", dir)
		}
	}
}

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findProjectRoot(nested); got != root {
		t.Errorf("findProjectRoot = %q, want %q", got, root)
	}
}
