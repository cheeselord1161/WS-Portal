package workspace

import "testing"

func TestMergePreservesCuratedContent(t *testing.T) {
	existing := &Workspace{
		Version:   CurrentVersion,
		Workspace: Identity{Name: "app", Description: "My curated environment"},
		Project: &Project{
			Name:   "app",
			Path:   "${WORKSPACE_ROOT}/app",
			Source: &Source{Type: "git", URL: "git@example.com:app.git", Branch: "main"},
		},
		Applications: []Application{
			{ID: "vscode", Name: "vscode", Open: []string{"${HOME}/projects/app"}},
			{ID: "postman", Name: "postman"},
		},
		Browser: []BrowserSession{
			{Browser: "chrome", Windows: []BrowserWindow{
				{Tabs: []BrowserTab{{URL: "https://keep.example", Title: "Keep"}}},
			}},
		},
		Services: []Service{{Name: "postgres", Version: "16", Required: true}},
		Environment: Environment{
			Runtime: map[string]string{"node": "22"},
			Tools:   []string{"git"},
		},
		Metadata: Metadata{CreatedBy: "hand", CreatedAt: "2020-01-01T00:00:00Z"},
	}

	captured := &Workspace{
		Version:   CurrentVersion,
		Workspace: Identity{Name: "app", Description: "Captured from the running environment"},
		Project:   &Project{Name: "app", Path: "${HOME}/projects/app"},
		Applications: []Application{
			{ID: "vscode", Name: "vscode", Open: []string{"${HOME}/projects/app/sub"}, WorkingDirectory: "${HOME}/projects/app/sub"},
		},
		Browser: []BrowserSession{
			{Browser: "chrome", Windows: []BrowserWindow{
				{Tabs: []BrowserTab{{URL: "https://new.example"}}},
			}},
			{Browser: "firefox", Windows: []BrowserWindow{
				{Tabs: []BrowserTab{{URL: "https://mozilla.org", Title: "Mozilla"}}},
			}},
		},
		Services: []Service{{Name: "postgres"}, {Name: "redis"}},
		Environment: Environment{
			Runtime: map[string]string{},
			Tools:   []string{"go"},
		},
		Metadata: Metadata{CreatedBy: "wsportal (capture)", CreatedAt: "2026-10-04T00:00:00Z"},
	}

	got := Merge(existing, captured)

	// Curated identity and project source survive; the live path wins.
	if got.Workspace.Description != "My curated environment" {
		t.Errorf("Description = %q", got.Workspace.Description)
	}
	if got.Project.Source == nil || got.Project.Source.URL != "git@example.com:app.git" {
		t.Errorf("Project.Source = %+v, want preserved", got.Project.Source)
	}
	if got.Project.Path != "${HOME}/projects/app" {
		t.Errorf("Project.Path = %q, want the captured path", got.Project.Path)
	}

	// Applications are unioned by ID; hand-added apps are kept.
	if len(got.Applications) != 2 {
		t.Fatalf("Applications = %+v, want 2", got.Applications)
	}
	wantOpen := []string{"${HOME}/projects/app", "${HOME}/projects/app/sub"}
	if !equalStrings(got.Applications[0].Open, wantOpen) {
		t.Errorf("vscode.Open = %v, want %v", got.Applications[0].Open, wantOpen)
	}

	// Browser tabs are unioned per browser.
	if len(got.Browser) != 2 {
		t.Fatalf("Browser = %+v, want chrome and firefox", got.Browser)
	}
	if !equalStrings(got.Browser[0].TabURLs(), []string{"https://keep.example", "https://new.example"}) {
		t.Errorf("chrome tabs = %v", got.Browser[0].TabURLs())
	}
	if !equalStrings(got.Browser[1].TabURLs(), []string{"https://mozilla.org"}) {
		t.Errorf("firefox tabs = %v", got.Browser[1].TabURLs())
	}

	// Service metadata from the existing workspace is preserved.
	postgres := findService(got, "postgres")
	if postgres == nil || postgres.Version != "16" || !postgres.Required {
		t.Errorf("postgres = %+v, want version 16 required", postgres)
	}
	if findService(got, "redis") == nil {
		t.Errorf("redis service missing: %+v", got.Services)
	}

	// Runtime pins survive; tools are unioned.
	if got.Environment.Runtime["node"] != "22" {
		t.Errorf("Runtime[node] = %q, want 22", got.Environment.Runtime["node"])
	}
	if !equalStrings(got.Environment.Tools, []string{"git", "go"}) {
		t.Errorf("Tools = %v, want [git go]", got.Environment.Tools)
	}

	// Original creation time is kept.
	if got.Metadata.CreatedAt != "2020-01-01T00:00:00Z" {
		t.Errorf("CreatedAt = %q", got.Metadata.CreatedAt)
	}
}

func TestMergeApplicationsByLogicalIdentity(t *testing.T) {
	// An existing workspace names VS Code explicitly; a fresh capture records
	// only the portable id. They describe the same application and must merge.
	existing := &Workspace{
		Version:   CurrentVersion,
		Workspace: Identity{Name: "x"},
		Applications: []Application{
			{ID: "editor", Name: "vscode", Open: []string{"${HOME}/a"}},
		},
	}
	captured := &Workspace{
		Version:      CurrentVersion,
		Workspace:    Identity{Name: "x"},
		Applications: []Application{{ID: "vscode", Open: []string{"${HOME}/b"}}},
	}

	got := Merge(existing, captured)
	if len(got.Applications) != 1 {
		t.Fatalf("Applications = %+v, want one merged entry", got.Applications)
	}
	if got.Applications[0].ID != "editor" {
		t.Errorf("ID = %q, want the curated id editor preserved", got.Applications[0].ID)
	}
	if want := []string{"${HOME}/a", "${HOME}/b"}; !equalStrings(got.Applications[0].Open, want) {
		t.Errorf("Open = %v, want %v", got.Applications[0].Open, want)
	}
}

func TestMergeWithNil(t *testing.T) {
	ws := New("x", "")
	if got := Merge(nil, ws); got != ws {
		t.Errorf("Merge(nil, ws) should return ws")
	}
	if got := Merge(ws, nil); got != ws {
		t.Errorf("Merge(ws, nil) should return ws")
	}
}

func findService(ws *Workspace, name string) *Service {
	for i := range ws.Services {
		if ws.Services[i].Name == name {
			return &ws.Services[i]
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
