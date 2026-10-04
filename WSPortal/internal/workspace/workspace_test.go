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
	if len(got.Applications) != 1 || got.Applications[0].Name != "vscode" {
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
