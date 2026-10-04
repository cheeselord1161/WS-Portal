// Package restore turns a workspace into a plan, and (eventually) executes
// that plan to reconstruct the environment on this machine.
//
// The two phases are deliberately separate. Planning is read-only and safe:
// it answers "what would happen?". Execution changes the machine and is where
// confirmations, dependency checks, and error handling belong. The CLI is
// written against the interface below so a real execution engine can be
// dropped in without restructuring commands.
package restore

import (
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// StepStatus describes the outcome of planning a single step.
type StepStatus string

const (
	// StatusReady means the step can be executed.
	StatusReady StepStatus = "ready"
	// StatusMissing means a required dependency is missing.
	StatusMissing StepStatus = "missing"
	// StatusSkipped means the step was not planned.
	StatusSkipped StepStatus = "skipped"
	// StatusUnsupported means the platform adapter cannot perform the step yet.
	StatusUnsupported StepStatus = "unsupported"
)

// StepKind identifies what a plan step does.
type StepKind string

const (
	// StepProject locates or clones the project.
	StepProject StepKind = "project"
	// StepApplication launches a desktop application.
	StepApplication StepKind = "application"
	// StepBrowser opens browser tabs.
	StepBrowser StepKind = "browser"
	// StepTerminal opens a terminal, possibly running a command.
	StepTerminal StepKind = "terminal"
	// StepService ensures a service is running.
	StepService StepKind = "service"
)

// Step is one action in a restore plan.
type Step struct {
	// Kind categorizes the step.
	Kind StepKind
	// Label is a short human-readable description, e.g. "VS Code".
	Label string
	// Detail explains the step further, e.g. the command to run.
	Detail string
	// Status is the planning outcome for this step.
	Status StepStatus
	// RequiresConfirmation is true when executing the step would run a command
	// from an untrusted workspace.
	RequiresConfirmation bool
	// Reason explains a non-ready status.
	Reason string

	// Open lists the resolved paths an application step should open.
	Open []string
	// Tabs lists the resolved URLs a browser step should open.
	Tabs []string
	// WorkingDirectory is the resolved directory a terminal or application
	// step should start in.
	WorkingDirectory string
	// Command is the resolved command a terminal step should run.
	Command string

	// ProjectPath is the resolved local path of a project step.
	ProjectPath string
	// SourceType, SourceURL, and SourceBranch describe how to obtain a
	// project's code, when a source is configured.
	SourceType   string
	SourceURL    string
	SourceBranch string
}

// Plan is an ordered list of steps describing how to restore a workspace.
type Plan struct {
	// WorkspaceName is the name of the workspace being restored.
	WorkspaceName string
	// Platform is the GOOS value the plan was produced for.
	Platform string
	// Steps are the planned actions, in execution order.
	Steps []Step
	// Dependencies lists the machine's tool and runtime checks, e.g.
	// "✓ git" or "✗ docker (not installed)".
	Dependencies []string
	// Warnings are non-fatal notes about the plan.
	Warnings []string
}

// RestoreEngine produces and executes restore plans.
type RestoreEngine interface {
	// Plan inspects the workspace and this machine and returns the steps that
	// would restore the environment. It must not change the machine.
	Plan(ws *workspace.Workspace) (*Plan, error)
	// Execute carries out a plan. Implementations must confirm destructive or
	// untrusted steps with the user before running them.
	Execute(plan *Plan) error
}
