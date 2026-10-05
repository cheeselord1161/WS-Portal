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

// --- application restoration ----------------------------------------------

// fakeApps is an in-memory AppLauncher that records launches instead of opening
// applications. It lets the tests assert what resume would do without touching
// the developer's machine.
type fakeApps struct {
	resolved map[string]platform.Application
	launched []launchCall
	err      error
}

type launchCall struct {
	app  string
	args []string
}

func (f *fakeApps) Resolve(app string) (platform.Application, error) {
	if f.err != nil {
		return platform.Application{}, f.err
	}
	if res, ok := f.resolved[app]; ok {
		return res, nil
	}
	return platform.Application{ID: platform.CanonicalAppID(app), Name: app}, nil
}

func (f *fakeApps) Launch(app string, args ...string) error {
	f.launched = append(f.launched, launchCall{app: app, args: append([]string(nil), args...)})
	return nil
}

func (f *fakeApps) Name() string    { return "fake" }
func (f *fakeApps) Supported() bool { return true }

// fakeTerminals records terminal launches.
type fakeTerminals struct {
	calls []terminalCall
}

type terminalCall struct {
	dir     string
	command string
}

func (f *fakeTerminals) Open(dir, command string) error {
	f.calls = append(f.calls, terminalCall{dir: dir, command: command})
	return nil
}

func (f *fakeTerminals) Name() string    { return "fake" }
func (f *fakeTerminals) Supported() bool { return true }

func appPlanner(apps platform.AppLauncher, terminals platform.TerminalLauncher) *Planner {
	return NewPlanner(&platform.Adapter{
		OS:        "linux",
		Apps:      apps,
		Browsers:  platform.UnsupportedBrowsers{},
		Terminals: terminals,
		Services:  platform.UnsupportedServices{},
		Tools:     fakeTools{present: map[string]bool{}},
	}, workspace.NewMapResolver(map[string]string{"WORKSPACE_ROOT": "/root"}))
}

func appWorkspace(apps ...workspace.Application) *workspace.Workspace {
	ws := workspace.New("myproject", "")
	ws.Project = nil
	ws.Applications = apps
	return ws
}

func findStep(t *testing.T, plan *Plan, kind StepKind) Step {
	t.Helper()
	for _, step := range plan.Steps {
		if step.Kind == kind {
			return step
		}
	}
	t.Fatalf("plan has no %s step: %+v", kind, plan.Steps)
	return Step{}
}

func TestPlanApplicationUsesHumanNameAndReportsMissing(t *testing.T) {
	apps := &fakeApps{resolved: map[string]platform.Application{
		"vscode": {ID: "vscode", Name: "VS Code", Known: true, Installed: false},
	}}
	planner := appPlanner(apps, &fakeTerminals{})

	plan, err := planner.Plan(appWorkspace(workspace.Application{
		ID:   "vscode",
		Open: []string{"${WORKSPACE_ROOT}/p"},
	}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	step := findStep(t, plan, StepApplication)
	if step.Label != "VS Code" {
		t.Errorf("Label = %q, want %q", step.Label, "VS Code")
	}
	if step.AppID != "vscode" {
		t.Errorf("AppID = %q, want vscode", step.AppID)
	}
	if step.Status != StatusMissing {
		t.Errorf("Status = %q, want missing", step.Status)
	}
	if !strings.Contains(step.Reason, "not installed") {
		t.Errorf("Reason = %q, want a not-installed explanation", step.Reason)
	}
	// Planning must not launch anything.
	if len(apps.launched) != 0 {
		t.Errorf("Plan launched %v, want no launches", apps.launched)
	}
}

func TestPlanUnknownApplicationIsReportedNotLaunched(t *testing.T) {
	apps := &fakeApps{}
	plan, err := appPlanner(apps, &fakeTerminals{}).Plan(appWorkspace(
		workspace.Application{ID: "mystery-app"},
	))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	step := findStep(t, plan, StepApplication)
	if step.Status != StatusMissing || !strings.Contains(step.Reason, "unrecognized") {
		t.Errorf("step = %+v, want an unrecognized-application warning", step)
	}
}

func TestExecuteLaunchesResolvedApplicationWithOpenPaths(t *testing.T) {
	apps := &fakeApps{resolved: map[string]platform.Application{
		"vscode": {ID: "vscode", Name: "VS Code", Known: true, Installed: true},
	}}
	planner := appPlanner(apps, &fakeTerminals{})
	plan, err := planner.Plan(appWorkspace(workspace.Application{
		ID:   "vscode",
		Open: []string{"${WORKSPACE_ROOT}/p"},
	}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var out bytes.Buffer
	if err := planner.Execute(plan, &out, NewScanner()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(apps.launched) != 1 {
		t.Fatalf("launched = %+v, want one launch", apps.launched)
	}
	got := apps.launched[0]
	if got.app != "vscode" {
		t.Errorf("launched app = %q, want vscode", got.app)
	}
	if len(got.args) != 1 || got.args[0] != "/root/p" {
		t.Errorf("launched args = %v, want [/root/p]", got.args)
	}
}

func TestExecuteSkipsMissingApplicationAndContinues(t *testing.T) {
	apps := &fakeApps{resolved: map[string]platform.Application{
		"vscode": {ID: "vscode", Name: "VS Code", Known: true, Installed: false},
	}}
	planner := appPlanner(apps, &fakeTerminals{})
	plan, err := planner.Plan(appWorkspace(workspace.Application{ID: "vscode"}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var out bytes.Buffer
	if err := planner.Execute(plan, &out, NewScanner()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(apps.launched) != 0 {
		t.Errorf("missing application was launched: %v", apps.launched)
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("output = %q, want a skip notice", out.String())
	}
}

func TestPlanRoutesTerminalApplicationToTerminalLauncher(t *testing.T) {
	terminals := &fakeTerminals{}
	planner := appPlanner(&fakeApps{}, terminals)
	plan, err := planner.Plan(appWorkspace(workspace.Application{
		ID:               "terminal",
		WorkingDirectory: "${WORKSPACE_ROOT}/p",
	}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	step := findStep(t, plan, StepTerminal)
	if step.AppID != "terminal" {
		t.Errorf("AppID = %q, want terminal", step.AppID)
	}
	if step.Status != StatusReady {
		t.Errorf("Status = %q, want ready", step.Status)
	}
	if step.WorkingDirectory != "/root/p" {
		t.Errorf("WorkingDirectory = %q, want /root/p", step.WorkingDirectory)
	}

	var out bytes.Buffer
	if err := planner.Execute(plan, &out, NewScanner()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(terminals.calls) != 1 || terminals.calls[0].dir != "/root/p" {
		t.Errorf("terminal calls = %+v, want one in /root/p", terminals.calls)
	}
}

func TestPlanWithoutApplicationsHasNoApplicationStep(t *testing.T) {
	plan, err := appPlanner(&fakeApps{}, &fakeTerminals{}).Plan(appWorkspace())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, step := range plan.Steps {
		if step.Kind == StepApplication {
			t.Errorf("unexpected application step: %+v", step)
		}
	}
}
