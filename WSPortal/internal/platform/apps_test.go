package platform_test

import (
	"testing"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/platform/detect"
)

func TestLookupApplicationResolvesLogicalIDsAndAliases(t *testing.T) {
	cases := []struct {
		in   string
		want string
		name string
	}{
		{"vscode", "vscode", "VS Code"},
		{"VS Code", "vscode", "VS Code"},
		{"code", "vscode", "VS Code"},
		{"code.exe", "vscode", "VS Code"},
		{"/usr/bin/code", "vscode", "VS Code"},
		{"google-chrome", "chrome", "Google Chrome"},
		{"Google Chrome", "chrome", "Google Chrome"},
		{"terminal", "terminal", "Terminal"},
		{"Windows Terminal", "terminal", "Terminal"},
	}
	for _, tc := range cases {
		app, ok := platform.LookupApplication(tc.in)
		if !ok {
			t.Errorf("LookupApplication(%q) = not found", tc.in)
			continue
		}
		if app.ID != tc.want || app.Name != tc.name {
			t.Errorf("LookupApplication(%q) = %+v, want id %q name %q", tc.in, app, tc.want, tc.name)
		}
	}
}

func TestLookupApplicationUnknown(t *testing.T) {
	if _, ok := platform.LookupApplication("definitely-not-a-real-app"); ok {
		t.Error("unknown application should not be found")
	}
	if _, ok := platform.LookupApplication(""); ok {
		t.Error("empty name should not be found")
	}
}

func TestCanonicalAppID(t *testing.T) {
	cases := map[string]string{
		"code":               "vscode",
		"Visual Studio Code": "vscode",
		"Google Chrome":      "chrome",
		"":                   "",
		"custom-tool":        "custom-tool",
	}
	for in, want := range cases {
		if got := platform.CanonicalAppID(in); got != want {
			t.Errorf("CanonicalAppID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKnownApplicationsHaveUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, app := range platform.KnownApplications() {
		if app.ID == "" || app.Name == "" {
			t.Errorf("incomplete catalogue entry: %+v", app)
		}
		if seen[app.ID] {
			t.Errorf("duplicate catalogue id %q", app.ID)
		}
		seen[app.ID] = true
	}
}

// TestAdaptersResolveKnownApplications verifies that each OS adapter maps the
// catalogued ids to a platform representation without executing anything. The
// adapters are selected by GOOS, but resolution itself is read-only.
func TestAdaptersResolveKnownApplications(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		a := detect.For(goos)
		for _, id := range []string{"vscode", "chrome", "terminal"} {
			got, err := a.Apps.Resolve(id)
			if err != nil {
				t.Errorf("%s Resolve(%q): %v", goos, id, err)
				continue
			}
			if !got.Known {
				t.Errorf("%s Resolve(%q).Known = false, want true", goos, id)
			}
			if got.ID != id {
				t.Errorf("%s Resolve(%q).ID = %q, want %q", goos, id, got.ID, id)
			}
			if got.Name == "" {
				t.Errorf("%s Resolve(%q).Name is empty", goos, id)
			}
		}
	}
}

func TestAdaptersResolveUnknownIsNotLaunchable(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		a := detect.For(goos)
		got, err := a.Apps.Resolve("definitely-not-a-real-app")
		if err != nil {
			t.Errorf("%s Resolve(unknown): %v", goos, err)
			continue
		}
		if got.Known || got.Installed {
			t.Errorf("%s unknown resolved to Known=%v Installed=%v, want false false",
				goos, got.Known, got.Installed)
		}
		if err := a.Apps.Launch("definitely-not-a-real-app"); err == nil {
			t.Errorf("%s Launch(unknown) should fail", goos)
		}
	}
}
