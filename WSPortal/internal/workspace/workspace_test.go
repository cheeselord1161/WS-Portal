package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "myproject.ws")

	ws := New("myproject", "MyProject development environment")
	ws.Applications = []Application{
		{ID: "editor", Name: "vscode", Open: []string{"${WORKSPACE_ROOT}/myproject"}},
	}
	ws.Terminals = []Terminal{
		{Name: "server", WorkingDirectory: "${WORKSPACE_ROOT}/myproject", Command: "npm run dev"},
	}
	ws.Browser = []BrowserSession{
		{Browser: "chrome", Windows: []BrowserWindow{
			{Tabs: []BrowserTab{{URL: "http://localhost:3000", Title: "Dev server"}}},
		}},
	}
	ws.Services = []Service{
		{Name: "docker", Required: true},
		{Name: "postgres", Version: "16", Required: true},
	}
	ws.Environment.Runtime = map[string]string{"node": "22"}
	ws.Environment.Tools = []string{"git", "docker"}

	if err := Save(ws, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Name() != "myproject" {
		t.Errorf("Name() = %q, want %q", got.Name(), "myproject")
	}
	if got.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if len(got.Applications) != 1 || got.Applications[0].ID != "editor" || got.Applications[0].Name != "vscode" {
		t.Errorf("Applications = %+v", got.Applications)
	}
	if len(got.Terminals) != 1 || got.Terminals[0].Command != "npm run dev" {
		t.Errorf("Terminals = %+v", got.Terminals)
	}
	if len(got.Browser) != 1 || len(got.Browser[0].TabURLs()) != 1 {
		t.Errorf("Browser = %+v", got.Browser)
	}
	if title := got.Browser[0].Windows[0].Tabs[0].Title; title != "Dev server" {
		t.Errorf("tab title = %q, want %q", title, "Dev server")
	}
	if len(got.Services) != 2 {
		t.Errorf("Services = %+v", got.Services)
	}
	if got.Environment.Runtime["node"] != "22" {
		t.Errorf("Runtime[node] = %q, want %q", got.Environment.Runtime["node"], "22")
	}
}

func TestLoadIDOnlyApplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "apps.ws")

	// The portable form uses the logical id and no name.
	doc := "version: 2\n" +
		"workspace:\n  name: apps\n" +
		"applications:\n" +
		"  - id: vscode\n" +
		"    open:\n" +
		"      - ${WORKSPACE_ROOT}/myproject\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Applications) != 1 {
		t.Fatalf("Applications = %+v, want one", got.Applications)
	}
	app := got.Applications[0]
	if app.ID != "vscode" || app.Name != "" {
		t.Errorf("application = %+v, want id vscode and no name", app)
	}
	if app.LogicalName() != "vscode" {
		t.Errorf("LogicalName() = %q, want vscode", app.LogicalName())
	}
	if want := "${WORKSPACE_ROOT}/myproject"; len(app.Open) != 1 || app.Open[0] != want {
		t.Errorf("Open = %v, want [%s]", app.Open, want)
	}

	// Round-trip: the id and portable path must survive a save/load.
	if err := Save(got, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(again.Applications) != 1 || again.Applications[0].ID != "vscode" || again.Applications[0].Name != "" {
		t.Errorf("round-tripped application = %+v", again.Applications)
	}
	if want := "${WORKSPACE_ROOT}/myproject"; again.Applications[0].Open[0] != want {
		t.Errorf("round-tripped open = %q, want %q", again.Applications[0].Open[0], want)
	}
}

func TestApplicationLogicalNamePrefersNameThenID(t *testing.T) {
	cases := []struct {
		app  Application
		want string
	}{
		{Application{ID: "vscode"}, "vscode"},
		{Application{ID: "editor", Name: "vscode"}, "vscode"},
		{Application{ID: "editor", Name: "  vscode  "}, "vscode"},
		{Application{ID: "terminal", Name: ""}, "terminal"},
	}
	for _, tc := range cases {
		if got := tc.app.LogicalName(); got != tc.want {
			t.Errorf("LogicalName(%+v) = %q, want %q", tc.app, got, tc.want)
		}
	}
}

func TestTabURLsDeduplicates(t *testing.T) {
	session := BrowserSession{Windows: []BrowserWindow{
		{Tabs: []BrowserTab{{URL: "https://a"}, {URL: "https://b"}}},
		{Tabs: []BrowserTab{{URL: "https://a"}}},
	}}
	want := []string{"https://a", "https://b"}
	got := session.TabURLs()
	if len(got) != len(want) {
		t.Fatalf("TabURLs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TabURLs = %v, want %v", got, want)
			break
		}
	}
}

func TestLoadMigratesLegacyTabs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.ws")

	// A version 1 document with the old flat browser tab list.
	doc := "version: 1\n" +
		"workspace:\n  name: legacy\n" +
		"browser:\n" +
		"  - browser: firefox\n" +
		"    tabs:\n" +
		"      - https://example.com\n" +
		"      - https://example.org\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if len(got.Browser) != 1 {
		t.Fatalf("Browser = %+v", got.Browser)
	}
	if len(got.Browser[0].Windows) != 1 || len(got.Browser[0].Windows[0].Tabs) != 2 {
		t.Fatalf("legacy tabs not migrated to one window: %+v", got.Browser[0])
	}
	if len(got.Browser[0].Tabs) != 0 {
		t.Errorf("legacy Tabs field should be cleared, got %v", got.Browser[0].Tabs)
	}
}

func TestSaveAppendsExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bare")

	if err := Save(New("bare", ""), path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := Load(path + Extension); err != nil {
		t.Fatalf("Load with appended extension: %v", err)
	}
}

func TestLoadRejectsWrongExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thing.yaml")
	if err := Save(New("thing", ""), path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a non-.ws file")
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	doc := "version: 1\nworkspace:\n  name: x\nbogus_field: 3\n"
	if _, err := Decode(strings.NewReader(doc)); err == nil {
		t.Fatal("Decode should reject unknown fields")
	}
}

func TestDecodeEmpty(t *testing.T) {
	if _, err := Decode(strings.NewReader("")); err == nil {
		t.Fatal("Decode should reject an empty document")
	}
}

func TestHasExtension(t *testing.T) {
	cases := map[string]bool{
		"a.ws":   true,
		"a.WS":   true,
		" a.ws ": true,
		"a.yaml": false,
		"":       false,
		"ws":     false,
	}
	for in, want := range cases {
		if got := HasExtension(in); got != want {
			t.Errorf("HasExtension(%q) = %v, want %v", in, got, want)
		}
	}
}
