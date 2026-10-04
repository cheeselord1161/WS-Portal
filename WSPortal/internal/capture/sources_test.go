package capture

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEditorSourceVSCodeWorkspaces(t *testing.T) {
	userDir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		dir := filepath.Join(userDir, "workspaceStorage", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workspace.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a", `{"folder":"file:///home/u/projects/app"}`)
	write("b", `{"workspace":"file:///home/u/projects/team.code-workspace"}`)
	write("c", `not json`)

	src := EditorSource{Roots: []editorRoot{{App: "vscode", Dir: userDir, Layout: layoutVSCode}}}
	obs, err := src.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 1 || obs[0].Name != "vscode" || obs[0].Kind != KindApplication {
		t.Fatalf("observations = %+v", obs)
	}
	want := []string{"/home/u/projects/app", "/home/u/projects/team.code-workspace"}
	if !reflect.DeepEqual(obs[0].Values, want) {
		t.Errorf("Values = %v, want %v", obs[0].Values, want)
	}
}

func TestEditorSourceJetBrainsRecentProjects(t *testing.T) {
	root := t.TempDir()
	options := filepath.Join(root, "GoLand2024.1", "options")
	if err := os.MkdirAll(options, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `<?xml version="1.0"?>
<application>
  <component name="RecentProjectsManager">
    <option name="additionalInfo">
      <map>
        <entry key="/home/u/projects/api" />
        <entry key="/home/u/projects/web" />
      </map>
    </option>
  </component>
</application>`
	if err := os.WriteFile(filepath.Join(options, "recentProjects.xml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	src := EditorSource{Roots: []editorRoot{{Dir: root, Layout: layoutJetBrains}}}
	obs, err := src.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 1 || obs[0].Name != "goland" {
		t.Fatalf("observations = %+v, want one goland", obs)
	}
	want := []string{"/home/u/projects/api", "/home/u/projects/web"}
	if !reflect.DeepEqual(obs[0].Values, want) {
		t.Errorf("Values = %v, want %v", obs[0].Values, want)
	}
}

func TestBrowserSourceExtractsTabs(t *testing.T) {
	base := t.TempDir()
	sessions := filepath.Join(base, "Default", "Sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}

	// A stand-in for a Chromium session file: URLs stored verbatim between
	// binary fields.
	var data []byte
	data = append(data, []byte("SNSS\x03\x00\x00\x00")...)
	data = append(data, []byte("https://github.com/cheeselord1161/WS_Portal")...)
	data = append(data, 0x00, 0x01, 0xff)
	data = append(data, []byte("https://example.com/docs")...)
	data = append(data, 0x00)
	data = append(data, []byte("https://cdn.example.com/app.js")...)
	data = append(data, 0x00)
	if err := os.WriteFile(filepath.Join(sessions, "Current Session"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	src := BrowserSource{Bases: []browserBase{{Browser: "chrome", Dir: base}}}
	obs, err := src.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 1 || obs[0].Name != "chrome" || obs[0].Kind != KindBrowser {
		t.Fatalf("observations = %+v", obs)
	}
	want := []string{"https://github.com/cheeselord1161/WS_Portal", "https://example.com/docs"}
	// Chromium session files carry no titles or window grouping, so the tabs
	// land in a single window with empty titles.
	if got := tabURLs(obs[0].Tabs); !reflect.DeepEqual(got, want) {
		t.Errorf("tabs = %v, want %v", got, want)
	}
	for _, tab := range obs[0].Tabs {
		if tab.Window != 0 || tab.Title != "" {
			t.Errorf("tab %+v, want window 0 and no title", tab)
		}
	}
}

func TestFirefoxSourceExtractsTabs(t *testing.T) {
	root := t.TempDir()
	backups := filepath.Join(root, "abc.default-release", "sessionstore-backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two windows. Window 0 holds a tab whose index selects its second entry
	// (so github is history, not an open tab) plus a titled tab; static/
	// internal URLs are filtered out. Window 1 holds one titled tab.
	store := `{"windows":[{"tabs":[` +
		`{"entries":[{"url":"https://github.com/cheeselord1161/WS_Portal"},{"url":"https://example.com/docs","title":"Docs"}],"index":2},` +
		`{"entries":[{"url":"https://news.ycombinator.com","title":"Hacker News"}],"index":1},` +
		`{"entries":[{"url":"https://cdn.example.com/app.js"}],"index":1},` +
		`{"entries":[{"url":"about:config"}],"index":1}` +
		`]},{"tabs":[` +
		`{"entries":[{"url":"https://mozilla.org","title":"Mozilla"}],"index":1}` +
		`]}]}`
	if err := os.WriteFile(filepath.Join(backups, "recovery.jsonlz4"), mozlz4Blob(t, []byte(store)), 0o644); err != nil {
		t.Fatal(err)
	}

	src := BrowserSource{FirefoxDirs: []string{root}}
	obs, err := src.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 1 || obs[0].Name != "firefox" || obs[0].Kind != KindBrowser {
		t.Fatalf("observations = %+v", obs)
	}
	want := []TabObservation{
		{URL: "https://example.com/docs", Title: "Docs", Window: 0},
		{URL: "https://news.ycombinator.com", Title: "Hacker News", Window: 0},
		{URL: "https://mozilla.org", Title: "Mozilla", Window: 1},
	}
	if !reflect.DeepEqual(obs[0].Tabs, want) {
		t.Errorf("tabs = %+v, want %+v", obs[0].Tabs, want)
	}
}

func TestBrowserSourceOmitsMissingFirefox(t *testing.T) {
	// A Firefox root with no readable profiles (e.g. Firefox is not installed)
	// must not produce an empty firefox session.
	root := t.TempDir()
	src := BrowserSource{FirefoxDirs: []string{root}}
	obs, err := src.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("observations = %+v, want none", obs)
	}
}

// tabURLs flattens tab observations to their URLs for assertions.
func tabURLs(tabs []TabObservation) []string {
	var out []string
	for _, tab := range tabs {
		out = append(out, tab.URL)
	}
	return out
}

func TestDecompressMozlz4RejectsForeignData(t *testing.T) {
	if _, err := decompressMozlz4([]byte("not a mozlz4 file at all")); err == nil {
		t.Fatal("decompressMozlz4 should reject data without the magic header")
	}
}

func TestDecompressMozlz4RoundTrip(t *testing.T) {
	want := []byte(`{"hello":"world"}`)
	got, err := decompressMozlz4(mozlz4Blob(t, want))
	if err != nil {
		t.Fatalf("decompressMozlz4: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("decompressed = %q, want %q", got, want)
	}
}

// mozlz4Blob builds a Firefox mozlz4 container around data for tests.
func mozlz4Blob(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("mozLZ40\x00")
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	buf.Write(size[:])
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractSessionURLsNormalizesAndFilters(t *testing.T) {
	data := []byte("x https://github.com/ https://github.com \x00 " +
		"https://chatgpt.com/* \x00 https://site.example/docs/ \x00 " +
		"https://site.example/app.js \x00")

	got := extractSessionURLs(data)
	want := []string{"https://github.com", "https://site.example/docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractSessionURLs = %v, want %v", got, want)
	}
}

func TestExtractSessionURLsStripsTrackingParams(t *testing.T) {
	data := []byte("https://ex.example/page?q=hello&utm_source=news&gclid=abc\x00")
	got := extractSessionURLs(data)
	want := []string{"https://ex.example/page?q=hello"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractSessionURLs = %v, want %v", got, want)
	}
}

func TestBuildAggregatesObservations(t *testing.T) {
	e := &ProcessEngine{HomeDir: "/home/u"}
	observations := []Observation{
		{Kind: KindApplication, Name: "vscode", Values: []string{"/home/u/p"}},
		{Kind: KindApplication, Name: "vscode", Values: []string{"/home/u/q"}},
		{Kind: KindBrowser, Name: "chrome", Tabs: []TabObservation{
			{URL: "https://a.example", Title: "A", Window: 0},
			{URL: "https://b.example", Window: 0},
		}},
	}

	ws := e.build("x", observations)

	if len(ws.Applications) != 1 {
		t.Fatalf("Applications = %+v, want one", ws.Applications)
	}
	wantOpen := []string{"${HOME}/p", "${HOME}/q"}
	if !reflect.DeepEqual(ws.Applications[0].Open, wantOpen) {
		t.Errorf("Open = %v, want %v", ws.Applications[0].Open, wantOpen)
	}
	if ws.Applications[0].WorkingDirectory != "${HOME}/p" {
		t.Errorf("WorkingDirectory = %q", ws.Applications[0].WorkingDirectory)
	}

	if len(ws.Browser) != 1 {
		t.Fatalf("Browser = %+v, want one session", ws.Browser)
	}
	if len(ws.Browser[0].Windows) != 1 || len(ws.Browser[0].Windows[0].Tabs) != 2 {
		t.Fatalf("Browser = %+v, want one window with two tabs", ws.Browser)
	}
	if got := ws.Browser[0].Windows[0].Tabs[0]; got.URL != "https://a.example" || got.Title != "A" {
		t.Errorf("first tab = %+v, want URL with title", got)
	}
}

func TestDisableSources(t *testing.T) {
	e := &ProcessEngine{Sources: []Source{
		ProcessSource{},
		BrowserSource{},
		EditorSource{},
	}}
	e.DisableSources("browsers")
	for _, s := range e.Sources {
		if s.Name() == "browsers" {
			t.Fatal("browsers source should have been removed")
		}
	}
	if len(e.Sources) != 2 {
		t.Errorf("Sources = %d, want 2", len(e.Sources))
	}
}
