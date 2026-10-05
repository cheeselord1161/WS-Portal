package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cheeselord1161/WS_Portal/internal/config"
	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/platform/detect"
	"github.com/cheeselord1161/WS_Portal/internal/validation"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

func newTestApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &App{
		Config: &config.Config{
			WorkspaceDir: t.TempDir(),
			Variables:    map[string]string{"WORKSPACE_ROOT": "/tmp/projects", "HOME": "/tmp"},
		},
		Platform:  detect.For("linux"),
		Validator: validation.New(),
		Out:       out,
		ErrOut:    errOut,
	}, out, errOut
}

func TestInitCreatesWorkspaceFile(t *testing.T) {
	app, out, _ := newTestApp(t)

	if err := app.ExecuteArgs([]string{"init", "myproject"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	path := filepath.Join(app.Config.WorkspaceDir, "myproject.ws")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("workspace file not created: %v", err)
	}
	if !strings.Contains(out.String(), "Created workspace") {
		t.Errorf("output = %q", out.String())
	}

	ws, err := workspace.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ws.Name() != "myproject" {
		t.Errorf("Name() = %q", ws.Name())
	}
	if ws.Project.Path != "${WORKSPACE_ROOT}/myproject" {
		t.Errorf("Project.Path = %q, want a portable path", ws.Project.Path)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	app, _, _ := newTestApp(t)

	if err := app.ExecuteArgs([]string{"init", "dup"}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := app.ExecuteArgs([]string{"init", "dup"}); err == nil {
		t.Fatal("second init should fail without --force")
	}
	if err := app.ExecuteArgs([]string{"init", "dup", "--force"}); err != nil {
		t.Fatalf("init --force: %v", err)
	}
}

func TestInspectShowsContents(t *testing.T) {
	app, out, _ := newTestApp(t)
	mustInit(t, app, "myproject")

	if err := app.ExecuteArgs([]string{"inspect", "myproject"}); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Workspace: myproject", "Project", "Version: 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestInspectShowsBrowserWindowsAndTitles(t *testing.T) {
	app, out, _ := newTestApp(t)
	path := filepath.Join(app.Config.WorkspaceDir, "tabs.ws")
	doc := "version: 2\n" +
		"workspace:\n  name: tabs\n" +
		"browser:\n" +
		"  - browser: firefox\n" +
		"    windows:\n" +
		"      - tabs:\n" +
		"          - url: https://example.com\n" +
		"            title: Example\n" +
		"      - tabs:\n" +
		"          - url: https://mozilla.org\n" +
		"            title: Mozilla\n"
	writeFile(t, path, doc)

	if err := app.ExecuteArgs([]string{"inspect", "tabs"}); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	got := out.String()
	for _, want := range []string{"window 1", "window 2", "https://example.com", "Example", "https://mozilla.org", "Mozilla"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestValidateReportsValid(t *testing.T) {
	app, out, _ := newTestApp(t)
	mustInit(t, app, "myproject")

	if err := app.ExecuteArgs([]string{"validate", "myproject"}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(out.String(), "Valid.") {
		t.Errorf("output = %q", out.String())
	}
}

func TestValidateReportsInvalid(t *testing.T) {
	app, _, _ := newTestApp(t)
	path := filepath.Join(app.Config.WorkspaceDir, "broken.ws")
	writeFile(t, path, "version: 1\nworkspace:\n  name: \"\"\n")

	err := app.ExecuteArgs([]string{"validate", path})
	if err == nil {
		t.Fatal("validate should fail on an invalid workspace")
	}
}

func TestResumePrintsPlan(t *testing.T) {
	app, out, _ := newTestApp(t)
	mustInit(t, app, "myproject")

	// Add a command-bearing terminal to exercise the confirmation marker.
	path := filepath.Join(app.Config.WorkspaceDir, "myproject.ws")
	ws, err := workspace.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ws.Terminals = []workspace.Terminal{{Name: "server", Command: "npm run dev"}}
	if err := workspace.Save(ws, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := app.ExecuteArgs([]string{"resume", "myproject"}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Workspace: myproject", "Restore plan", "requires confirmation", "--execute"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestListShowsWorkspaces(t *testing.T) {
	app, out, _ := newTestApp(t)

	if err := app.ExecuteArgs([]string{"list"}); err != nil {
		t.Fatalf("list (empty): %v", err)
	}
	if !strings.Contains(out.String(), "No workspaces found") {
		t.Errorf("empty output = %q", out.String())
	}

	mustInit(t, app, "alpha")
	mustInit(t, app, "beta")
	out.Reset()

	if err := app.ExecuteArgs([]string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("output = %q", got)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	app, _, _ := newTestApp(t)
	mustInit(t, app, "myproject")

	exportPath := filepath.Join(t.TempDir(), "shared.ws")
	if err := app.ExecuteArgs([]string{"export", "myproject", "--out", exportPath}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("exported file missing: %v", err)
	}

	if err := app.ExecuteArgs([]string{"import", exportPath, "--name", "imported"}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.Config.WorkspaceDir, "imported.ws")); err != nil {
		t.Fatalf("imported workspace missing: %v", err)
	}
}

func TestRemoteTransferIsNotExposed(t *testing.T) {
	app, _, _ := newTestApp(t)
	mustInit(t, app, "myproject")

	// Remote transfer is not implemented, so the CLI must not offer a flag or
	// a code argument for it. Only the LAN path is exposed.
	if err := app.ExecuteArgs([]string{"send", "myproject", "--remote"}); err == nil {
		t.Fatal("send --remote should not be a recognized flag")
	}
	if err := app.ExecuteArgs([]string{"receive", "7K4X-92QP"}); err == nil {
		t.Fatal("receive with a transfer code should be rejected")
	}
}

func TestCaptureWritesWorkspace(t *testing.T) {
	app, out, _ := newTestApp(t)

	if err := app.ExecuteArgs([]string{"capture", "captured"}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.Contains(out.String(), "Captured") {
		t.Errorf("capture output = %q", out.String())
	}
	path := filepath.Join(app.Config.WorkspaceDir, "captured.ws")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("captured workspace missing: %v", err)
	}
}

func TestCaptureMergesIntoExistingWorkspace(t *testing.T) {
	app, out, _ := newTestApp(t)
	mustInit(t, app, "captured")

	// Add content the capture engine cannot produce, so we can tell it was kept.
	path := filepath.Join(app.Config.WorkspaceDir, "captured.ws")
	ws, err := workspace.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ws.Services = []workspace.Service{{Name: "postgres", Version: "16", Required: true}}
	ws.Environment.Runtime = map[string]string{"node": "22"}
	if err := workspace.Save(ws, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out.Reset()
	if err := app.ExecuteArgs([]string{"capture", "captured"}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.Contains(out.String(), "merged") {
		t.Errorf("capture output = %q, want a merge notice", out.String())
	}

	got, err := workspace.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	foundPostgres := false
	for _, s := range got.Services {
		if s.Name == "postgres" {
			foundPostgres = true
			if s.Version != "16" || !s.Required {
				t.Errorf("postgres = %+v, want version 16 required", s)
			}
		}
	}
	if !foundPostgres {
		t.Errorf("Services = %+v, want postgres preserved", got.Services)
	}
	if got.Environment.Runtime["node"] != "22" {
		t.Errorf("Runtime[node] = %q, want 22", got.Environment.Runtime["node"])
	}
}

func TestCaptureOverwriteReplacesExisting(t *testing.T) {
	app, out, _ := newTestApp(t)
	mustInit(t, app, "captured")

	path := filepath.Join(app.Config.WorkspaceDir, "captured.ws")
	ws, err := workspace.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ws.Services = []workspace.Service{{Name: "postgres", Version: "16", Required: true}}
	if err := workspace.Save(ws, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out.Reset()
	if err := app.ExecuteArgs([]string{"capture", "captured", "--overwrite"}); err != nil {
		t.Fatalf("capture --overwrite: %v", err)
	}
	if strings.Contains(out.String(), "merged") {
		t.Errorf("output = %q, --overwrite should not merge", out.String())
	}

	got, err := workspace.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, s := range got.Services {
		if s.Name == "postgres" {
			t.Errorf("Services = %+v, --overwrite should have discarded postgres", got.Services)
		}
	}
}

// recordingApps is an AppLauncher that records launches instead of opening
// applications, so CLI tests never touch the developer's machine.
type recordingApps struct {
	launched []string
}

func (r *recordingApps) Resolve(app string) (platform.Application, error) {
	known, ok := platform.LookupApplication(app)
	id := platform.CanonicalAppID(app)
	name := id
	if ok {
		name = known.Name
	}
	return platform.Application{ID: id, Name: name, Known: ok, Installed: ok}, nil
}

func (r *recordingApps) Launch(app string, args ...string) error {
	r.launched = append(r.launched, app)
	return nil
}

func (r *recordingApps) Name() string    { return "recording" }
func (r *recordingApps) Supported() bool { return true }

// withRecordingApps installs a fake platform adapter that never launches
// applications for real.
func withRecordingApps(app *App) *recordingApps {
	apps := &recordingApps{}
	app.Platform = &platform.Adapter{
		OS:        "linux",
		Apps:      apps,
		Browsers:  platform.UnsupportedBrowsers{},
		Terminals: platform.UnsupportedTerminals{},
		Services:  platform.UnsupportedServices{},
		Tools:     platform.NewPathToolChecker(),
	}
	return apps
}

func TestResumePreviewDoesNotLaunchApplications(t *testing.T) {
	app, out, _ := newTestApp(t)
	apps := withRecordingApps(app)

	path := filepath.Join(app.Config.WorkspaceDir, "apps.ws")
	writeFile(t, path, "version: 2\n"+
		"workspace:\n  name: apps\n"+
		"applications:\n"+
		"  - id: vscode\n"+
		"    open:\n"+
		"      - ${WORKSPACE_ROOT}/myproject\n")

	if err := app.ExecuteArgs([]string{"resume", "apps"}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if len(apps.launched) != 0 {
		t.Errorf("preview launched %v, want nothing", apps.launched)
	}
	got := out.String()
	for _, want := range []string{"Applications:", "VS Code", "--execute"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestResumeExecuteLaunchesApplications(t *testing.T) {
	app, out, _ := newTestApp(t)
	apps := withRecordingApps(app)

	path := filepath.Join(app.Config.WorkspaceDir, "apps.ws")
	writeFile(t, path, "version: 2\n"+
		"workspace:\n  name: apps\n"+
		"applications:\n"+
		"  - id: vscode\n"+
		"    open:\n"+
		"      - ${WORKSPACE_ROOT}/myproject\n")

	if err := app.ExecuteArgs([]string{"resume", "apps", "--execute"}); err != nil {
		t.Fatalf("resume --execute: %v", err)
	}

	if len(apps.launched) != 1 || apps.launched[0] != "vscode" {
		t.Errorf("launched = %v, want [vscode]", apps.launched)
	}
	if !strings.Contains(out.String(), "Restore complete") {
		t.Errorf("output = %q", out.String())
	}
}

func TestUnknownWorkspaceErrors(t *testing.T) {
	app, _, _ := newTestApp(t)
	if err := app.ExecuteArgs([]string{"inspect", "nope"}); err == nil {
		t.Fatal("inspect of an unknown workspace should fail")
	}
}

func TestVersionCommand(t *testing.T) {
	app, out, _ := newTestApp(t)
	if err := app.ExecuteArgs([]string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "wsportal") {
		t.Errorf("output = %q", out.String())
	}
}

// --- helpers ---------------------------------------------------------------

func mustInit(t *testing.T, app *App, name string) {
	t.Helper()
	if err := app.ExecuteArgs([]string{"init", name}); err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
