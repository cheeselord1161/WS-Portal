package restore

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// fakeServices is an in-memory ServiceManager for tests.
type fakeServices struct {
	installed map[string]bool
	running   map[string]bool
}

func (f fakeServices) IsInstalled(name string) bool { return f.installed[name] }
func (f fakeServices) IsRunning(name string) bool   { return f.running[name] }
func (f fakeServices) Start(string) error           { return nil }
func (f fakeServices) Name() string                 { return "fake" }
func (f fakeServices) Supported() bool              { return true }

// fakeTools is an in-memory ToolChecker for tests.
type fakeTools struct {
	present map[string]bool
}

func (f fakeTools) LookPath(name string) (string, bool) {
	if f.present[name] {
		return "/usr/bin/" + name, true
	}
	return "", false
}

func (f fakeTools) Ordered() []string {
	out := make([]string, 0, len(f.present))
	for name := range f.present {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func testWorkspace() *workspace.Workspace {
	ws := workspace.New("myproject", "desc")
	ws.Environment.Tools = []string{"git", "docker"}
	ws.Applications = []workspace.Application{
		{ID: "editor", Name: "vscode", Open: []string{"${WORKSPACE_ROOT}/p"}},
	}
	ws.Services = []workspace.Service{{Name: "docker", Required: true}}
	return ws
}

func testPlanner() *Planner {
	return NewPlanner(&platform.Adapter{
		OS:        "linux",
		Apps:      platform.UnsupportedApps{},
		Browsers:  platform.UnsupportedBrowsers{},
		Terminals: platform.UnsupportedTerminals{},
		Services: fakeServices{
			installed: map[string]bool{"docker": true},
			running:   map[string]bool{"docker": true},
		},
		Tools: fakeTools{present: map[string]bool{"git": true}},
	}, workspace.NewMapResolver(map[string]string{"WORKSPACE_ROOT": "/root"}))
}

func TestPlanBuildsSteps(t *testing.T) {
	plan, err := testPlanner().Plan(testWorkspace())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.WorkspaceName != "myproject" {
		t.Errorf("WorkspaceName = %q", plan.WorkspaceName)
	}
	if len(plan.Steps) == 0 {
		t.Fatal("plan has no steps")
	}
}

func TestExecutePrintsEachStep(t *testing.T) {
	// A plan is passed directly so the executor drives the steps.
	plan := &Plan{
		Steps: []Step{
			{Kind: StepProject, Label: "myproject"},
			{Kind: StepApplication, Label: "vscode", RequiresConfirmation: true},
			{Kind: StepTerminal, Label: "server"},
			{Kind: StepService, Label: "docker"},
		},
	}

	var out bytes.Buffer
	engine := NewPlanner(testPlanner().Platform, testPlanner().Resolver)

	ans := NewScanner("y", "n", "y")
	if err := engine.Execute(plan, &out, ans); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	for _, want := range []string{"project: myproject", "application", "terminal", "service"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestExecuteSkipsUnconfirmedSteps(t *testing.T) {
	plan := &Plan{
		Steps: []Step{
			{Kind: StepApplication, Label: "vscode", RequiresConfirmation: true},
			{Kind: StepService, Label: "docker"},
		},
	}

	var out bytes.Buffer
	ans := NewScanner("n", "y")
	if err := testPlanner().Execute(plan, &out, ans); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "application") {
		t.Errorf("confirmed application despite 'n' answer:\n%s", got)
	}
	if !strings.Contains(got, "service: docker") {
		t.Errorf("service step was not executed:\n%s", got)
	}
}

func TestExecuteNilPlan(t *testing.T) {
	var out bytes.Buffer
	err := testPlanner().Execute(nil, &out, NewScanner("y"))
	if err == nil {
		t.Fatal("Execute should reject a nil plan")
	}
}

func TestExecuteNilAnswers(t *testing.T) {
	var out bytes.Buffer
	err := testPlanner().Execute(&Plan{Steps: []Step{{Kind: StepProject}}}, &out, nil)
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("unexpected error: %v", err)
	}
}
