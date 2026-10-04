package workspace

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	r := NewMapResolver(map[string]string{"WORKSPACE_ROOT": "/home/alice/projects", "HOME": "/home/alice"})

	got, err := Resolve("${WORKSPACE_ROOT}/myproject", r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := "/home/alice/projects/myproject"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}

	got, err = Resolve("${HOME}/${WORKSPACE_ROOT}", NewMapResolver(map[string]string{"HOME": "a", "WORKSPACE_ROOT": "b"}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := "a/b"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveUndefinedVariable(t *testing.T) {
	_, err := Resolve("${MISSING}/x", NewMapResolver(nil))
	if err == nil {
		t.Fatal("Resolve should fail on an undefined variable")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("error should name the missing variable, got %v", err)
	}
}

func TestResolveIgnoresShellSyntax(t *testing.T) {
	// ${VAR:-default} is not a portable variable reference and must be left
	// untouched rather than mis-parsed.
	in := "${VAR:-default}"
	got, err := Resolve(in, NewMapResolver(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != in {
		t.Errorf("Resolve(%q) = %q, want unchanged", in, got)
	}
}

func TestResolveDefaults(t *testing.T) {
	r := NewMapResolver(map[string]string{"HOME": "/home/alice"}, map[string]string{"PATH": "/usr/bin", "NOPE": "/default"})
	want := "/usr/bin:/default"
	got, err := Resolve("${PATH:-/usr/bin}:${NOPE:-/default}", r)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("Resolve(%q) = %q, want %q", "${PATH:-/usr/bin}:${NOPE:-/default}", got, want)
	}
}

func TestVariables(t *testing.T) {
	got := Variables("${A}/x/${B}/${A}")
	want := []string{"A", "B"}
	if len(got) != len(want) {
		t.Fatalf("Variables = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Variables[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRequiredVariables(t *testing.T) {
	ws := New("p", "")
	ws.Applications = []Application{
		{ID: "editor", Name: "vscode", Open: []string{"${WORKSPACE_ROOT}/p"}},
	}
	ws.Terminals = []Terminal{{Name: "t", WorkingDirectory: "${PROJECTS}/p"}}

	vars := ws.RequiredVariables()
	if len(vars) != 2 {
		t.Fatalf("RequiredVariables = %v, want 2 entries", vars)
	}
	if vars[0] != "WORKSPACE_ROOT" || vars[1] != "PROJECTS" {
		t.Errorf("RequiredVariables = %v", vars)
	}
}

func TestResolvePathsDoesNotMutateOriginal(t *testing.T) {
	ws := New("p", "")
	ws.Project.Path = "${WORKSPACE_ROOT}/p"
	ws.Applications = []Application{{ID: "e", Name: "vscode", Open: []string{"${WORKSPACE_ROOT}/p"}}}

	r := NewMapResolver(map[string]string{"WORKSPACE_ROOT": "/root"})
	got, err := ws.ResolvePaths(r)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}

	if got.Project.Path != "/root/p" {
		t.Errorf("resolved path = %q, want %q", got.Project.Path, "/root/p")
	}
	if got.Applications[0].Open[0] != "/root/p" {
		t.Errorf("resolved open = %q", got.Applications[0].Open[0])
	}
	// The original must keep its portable form.
	if ws.Project.Path != "${WORKSPACE_ROOT}/p" {
		t.Errorf("original was mutated: %q", ws.Project.Path)
	}
	if ws.Applications[0].Open[0] != "${WORKSPACE_ROOT}/p" {
		t.Errorf("original applications were mutated: %q", ws.Applications[0].Open[0])
	}
}

func TestResolvePathsErrorMentionsField(t *testing.T) {
	ws := New("p", "")
	ws.Project.Path = ""
	ws.Applications = []Application{{ID: "e", Name: "vscode", Open: []string{"${NOPE}/x"}}}

	_, err := ws.ResolvePaths(NewMapResolver(nil))
	if err == nil {
		t.Fatal("ResolvePaths should fail")
	}
	if !strings.Contains(err.Error(), "applications[0].open[0]") {
		t.Errorf("error should name the field, got %v", err)
	}
}

func TestDebugResolve(t *testing.T) {
	r := NewMapResolver(map[string]string{"WORKSPACE_ROOT": "/home/alice/projects"})
	s := "${WORKSPACE_ROOT}/myproject"
	val, ok := r.Lookup("WORKSPACE_ROOT")
	t.Logf("resolver Lookup: %q, ok=%v", val, ok)
	got, err := Resolve(s, r)
	t.Logf("Resolve result: %q, err=%v", got, err)
}

func TestDebugResolveMatch(t *testing.T) {
	s := "${WORKSPACE_ROOT}/myproject"
	matches := varPattern.FindAllStringSubmatchIndex(s, -1)
	t.Logf("matches=%v", matches)
	for _, m := range matches {
		t.Logf("  full=[%d,%d) name=[%d,%d) def=[%d,%d)", m[0], m[1], m[2], m[3], m[4], m[5])
	}
}

func TestPrintRegexPattern(t *testing.T) {
	t.Logf("varPattern.String()=%q", varPattern.String())
	t.Logf("varPattern.String()=%s", varPattern.String())
}

func TestDebugResolveDefault(t *testing.T) {
	s := "${PATH:-/usr/bin}:${NOPE:-/default}"
	matches := varPattern.FindAllStringSubmatchIndex(s, -1)
	t.Logf("matches=%v", matches)
	for _, m := range matches {
		t.Logf("  full=[%d,%d) name=[%d,%d) def=[%d,%d)", m[0], m[1], m[2], m[3], m[4], m[5])
		t.Logf("  name=%q", s[m[2]:m[3]])
		t.Logf("  default=%q", s[m[4]:m[5]])
	}
}
