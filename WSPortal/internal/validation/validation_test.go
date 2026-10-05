package validation

import (
	"strings"
	"testing"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

func validWorkspace() *workspace.Workspace {
	ws := workspace.New("myproject", "desc")
	ws.Project.Source = &workspace.Source{
		Type:   "git",
		URL:    "https://github.com/user/myproject.git",
		Branch: "main",
	}
	ws.Applications = []workspace.Application{
		{ID: "editor", Name: "vscode"},
	}
	ws.Services = []workspace.Service{{Name: "docker", Required: true}}
	return ws
}

func TestValidateValidWorkspace(t *testing.T) {
	result := New().Validate(validWorkspace())
	if !result.OK() {
		t.Fatalf("expected valid workspace, got errors: %v", result.Errors())
	}
}

func TestValidateNil(t *testing.T) {
	result := New().Validate(nil)
	if result.OK() {
		t.Fatal("nil workspace should be invalid")
	}
}

func TestValidateMissingName(t *testing.T) {
	ws := validWorkspace()
	ws.Workspace.Name = ""
	result := New().Validate(ws)
	if result.OK() {
		t.Fatal("missing workspace name should be an error")
	}
	if !hasIssue(result, "workspace.name", SeverityError) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateFutureVersion(t *testing.T) {
	ws := validWorkspace()
	ws.Version = workspace.CurrentVersion + 1
	result := New().Validate(ws)
	if !hasIssue(result, "version", SeverityError) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateGitRequiresURL(t *testing.T) {
	ws := validWorkspace()
	ws.Project.Source = &workspace.Source{Type: "git"}
	result := New().Validate(ws)
	if !hasIssue(result, "project.source.url", SeverityError) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateInvalidGitURL(t *testing.T) {
	ws := validWorkspace()
	ws.Project.Source = &workspace.Source{Type: "git", URL: "not a url"}
	result := New().Validate(ws)
	if !hasIssue(result, "project.source.url", SeverityError) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateAcceptsScpStyleGitURL(t *testing.T) {
	ws := validWorkspace()
	ws.Project.Source = &workspace.Source{Type: "git", URL: "git@github.com:user/myproject.git"}
	if result := New().Validate(ws); !result.OK() {
		t.Errorf("scp-style URL should be valid, got %v", result.Errors())
	}
}

func TestValidateDuplicateApplicationID(t *testing.T) {
	ws := validWorkspace()
	ws.Applications = []workspace.Application{
		{ID: "editor", Name: "vscode"},
		{ID: "editor", Name: "terminal"},
	}
	result := New().Validate(ws)
	if !hasIssue(result, "applications[1].id", SeverityError) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateCommandProducesWarning(t *testing.T) {
	ws := validWorkspace()
	ws.Terminals = []workspace.Terminal{{Name: "server", Command: "npm run dev"}}
	result := New().Validate(ws)
	if !result.OK() {
		t.Fatalf("a command should be a warning, not an error: %v", result.Errors())
	}
	if !hasIssue(result, "terminals[0].command", SeverityWarning) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateRestrictedApplications(t *testing.T) {
	ws := validWorkspace()
	ws.Applications = []workspace.Application{{ID: "e", Name: "unknown-app"}}
	v := &Validator{KnownApplications: []string{"vscode"}}
	result := v.Validate(ws)
	if !hasIssue(result, "applications[0].name", SeverityWarning) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestValidateBrowserTabs(t *testing.T) {
	ws := validWorkspace()
	ws.Browser = []workspace.BrowserSession{
		{Browser: "chrome", Tabs: []string{"http://localhost:3000", "ftp://nope"}},
	}
	result := New().Validate(ws)
	if !hasIssue(result, "browser[0].tabs[1]", SeverityWarning) {
		t.Errorf("issues = %v", result.Issues)
	}
}

func TestResultFiltering(t *testing.T) {
	r := Result{Issues: []Issue{
		{Field: "a", Severity: SeverityError},
		{Field: "b", Severity: SeverityWarning},
	}}
	if len(r.Errors()) != 1 || len(r.Warnings()) != 1 {
		t.Errorf("filtering failed: %+v", r)
	}
	if r.OK() {
		t.Error("result with an error should not be OK")
	}
}

func hasIssue(r Result, field string, sev Severity) bool {
	for _, i := range r.Issues {
		if i.Field == field && i.Severity == sev {
			return true
		}
	}
	return false
}

func TestIssueString(t *testing.T) {
	i := Issue{Field: "project.name", Message: "is required"}
	if !strings.Contains(i.String(), "project.name") {
		t.Errorf("String() = %q", i.String())
	}
}
