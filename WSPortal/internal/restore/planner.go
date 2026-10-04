package restore

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// ErrConfirmation is returned by Execute when a confirmation is required and
// the caller chose not to provide approval.
var ErrConfirmation = fmt.Errorf("restoration requires confirmation")

// Scanner is a simple line reader for the confirmation prompt. It can be
// backed by canned answers (used by tests) or by a real reader such as stdin.
type Scanner struct {
	lines   []string
	i       int
	current string
	reader  interface {
		Scan() bool
		Text() string
	}
}

// NewScanner builds a Scanner from a list of pre-answered lines, most commonly
// one line containing "y" or "n".
func NewScanner(response ...string) *Scanner {
	return &Scanner{lines: append([]string{}, response...)}
}

// FromLines builds a Scanner from a slice of answers.
func FromLines(lines []string) *Scanner {
	return &Scanner{lines: append([]string{}, lines...)}
}

// NewReaderScanner builds a Scanner that reads one answer per line from r. It
// is used to prompt interactively, for example over os.Stdin.
func NewReaderScanner(r io.Reader) *Scanner {
	return &Scanner{reader: bufio.NewScanner(r)}
}

// Scan advances to the next answer and reports whether one was available.
func (s *Scanner) Scan() bool {
	if s == nil {
		return false
	}
	if s.reader != nil {
		if !s.reader.Scan() {
			return false
		}
		s.current = s.reader.Text()
		return true
	}
	if s.i >= len(s.lines) {
		return false
	}
	s.current = s.lines[s.i]
	s.i++
	return true
}

// Text returns the answer most recently read by Scan.
func (s *Scanner) Text() string {
	if s == nil {
		return ""
	}
	return s.current
}

// Planner is the RestoreEngine implementation. Planning is read-only: it
// inspects the workspace and this machine and never changes anything. Execute
// carries out a plan using the platform adapters and confirms steps that run
// untrusted commands.
type Planner struct {
	// Platform is the adapter for the current operating system.
	Platform *platform.Adapter
	// Resolver resolves portable variables such as ${WORKSPACE_ROOT}.
	Resolver workspace.Resolver
}

// NewPlanner returns a Planner for the given adapter and resolver.
func NewPlanner(p *platform.Adapter, r workspace.Resolver) *Planner {
	return &Planner{Platform: p, Resolver: r}
}

// capabilitySupported reports whether a platform capability is usable on this
// machine. A nil capability, or one that reports itself unsupported, means the
// planner marks the corresponding steps as unsupported.
func capabilitySupported(v any) bool {
	if v == nil {
		return false
	}
	if c, ok := v.(platform.Capability); ok {
		return c.Supported()
	}
	return true
}

// Plan builds a restore plan for the workspace.
func (p *Planner) Plan(ws *workspace.Workspace) (*Plan, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	goos := ""
	if p.Platform != nil {
		goos = p.Platform.OS
	}

	plan := &Plan{
		WorkspaceName: ws.Name(),
		Platform:      goos,
	}

	resolved, err := ws.ResolvePaths(p.Resolver)
	if err != nil {
		// A missing variable should not prevent showing the rest of the plan;
		// surface it as a warning and keep the portable originals visible.
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("could not resolve all paths: %v", err))
		resolved = ws
	}

	// --- Project ------------------------------------------------------------
	label := resolved.Project.Name
	if label == "" {
		label = "project"
	}
	step := Step{Kind: StepProject, Label: label, ProjectPath: resolved.Project.Path}
	if src := resolved.Project.Source; src != nil {
		step.SourceType = src.Type
		step.SourceURL = src.URL
		step.SourceBranch = src.Branch
	}
	switch {
	case step.ProjectPath != "" && pathExists(step.ProjectPath):
		step.Status = StatusReady
		step.Detail = step.ProjectPath
	case step.SourceURL != "":
		if strings.EqualFold(step.SourceType, "git") && !gitAvailable() {
			step.Status = StatusMissing
			step.Reason = "git is required to clone the project but is not installed"
			step.Detail = "clone from " + step.SourceURL
			break
		}
		step.Status = StatusReady
		step.Detail = "clone from " + step.SourceURL
		// Cloning runs a network command taken from the workspace file.
		step.RequiresConfirmation = true
	case step.ProjectPath != "":
		step.Status = StatusMissing
		step.Detail = step.ProjectPath
		step.Reason = "project path does not exist and no source is configured"
	default:
		step.Status = StatusMissing
		step.Reason = "no project path or source is configured"
	}
	plan.Steps = append(plan.Steps, step)

	// --- Applications -------------------------------------------------------
	for _, app := range resolved.Applications {
		step := Step{
			Kind:             StepApplication,
			Label:            app.Name,
			Open:             append([]string(nil), app.Open...),
			WorkingDirectory: app.WorkingDirectory,
		}
		if len(app.Open) > 0 {
			step.Detail = strings.Join(app.Open, ", ")
		}
		if p.Platform != nil && p.Platform.Apps != nil && capabilitySupported(p.Platform.Apps) {
			if p.Platform.Apps.Available(app.Name) {
				step.Status = StatusReady
			} else {
				step.Status = StatusMissing
				step.Reason = fmt.Sprintf("application %q is not installed", app.Name)
			}
		} else {
			step.Status = StatusUnsupported
			step.Reason = "application launching is not available on this platform"
		}
		plan.Steps = append(plan.Steps, step)
	}

	// --- Browser ------------------------------------------------------------
	for _, b := range resolved.Browser {
		urls := b.TabURLs()
		step := Step{
			Kind:  StepBrowser,
			Label: b.Browser,
			Tabs:  append([]string(nil), urls...),
		}
		if len(urls) > 0 {
			step.Detail = fmt.Sprintf("%d tab(s)", len(urls))
		}
		if p.Platform != nil && p.Platform.Browsers != nil && capabilitySupported(p.Platform.Browsers) {
			step.Status = StatusReady
		} else {
			step.Status = StatusUnsupported
			step.Reason = "browser launching is not available on this platform"
		}
		plan.Steps = append(plan.Steps, step)
	}

	// --- Terminals ----------------------------------------------------------
	for _, t := range resolved.Terminals {
		label := t.Name
		if label == "" {
			label = "terminal"
		}
		step := Step{
			Kind:             StepTerminal,
			Label:            label,
			WorkingDirectory: t.WorkingDirectory,
			Command:          t.Command,
		}
		if t.Command != "" {
			step.Detail = t.Command
			// Commands are untrusted input from the workspace file.
			step.RequiresConfirmation = true
		}
		if p.Platform != nil && p.Platform.Terminals != nil && capabilitySupported(p.Platform.Terminals) {
			step.Status = StatusReady
		} else {
			step.Status = StatusUnsupported
			step.Reason = "no terminal emulator is available on this system"
		}
		plan.Steps = append(plan.Steps, step)
	}

	// --- Services -----------------------------------------------------------
	for _, s := range resolved.Services {
		step := Step{Kind: StepService, Label: s.Name}
		if s.Version != "" {
			step.Detail = "version " + s.Version
		}
		if p.Platform != nil && p.Platform.Services != nil && p.Platform.Services.IsInstalled(s.Name) {
			step.Status = StatusReady
			if p.Platform.Services.IsRunning(s.Name) {
				step.Detail = strings.TrimSpace(step.Detail + " (running)")
			} else {
				step.Detail = strings.TrimSpace(step.Detail + " (stopped)")
			}
		} else if s.Required {
			step.Status = StatusMissing
			step.Reason = "required service is not installed"
		} else {
			step.Status = StatusSkipped
			step.Reason = "optional service is not installed"
		}
		plan.Steps = append(plan.Steps, step)
	}

	// --- Dependency checks --------------------------------------------------
	plan.Dependencies = append(plan.Dependencies, NewDependencyChecker(p.Platform, resolved).Check()...)

	return plan, nil
}

// Execute carries out a restore plan.
//
// It processes each step in order and asks for confirmation before any step
// that would run a command taken from an untrusted workspace. A step that
// cannot be executed (for example because the platform adapter does not
// support it) is reported and skipped so the rest of the restore can continue.
// userAnswers is a Scanner; in production it wraps stdin, in tests it is
// injected with canned answers.
func (p *Planner) Execute(plan *Plan, out io.Writer, userAnswers *Scanner) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if userAnswers == nil {
		return ErrConfirmation
	}
	outw := out
	if outw == nil {
		outw = io.Discard
	}

	for i, step := range plan.Steps {
		// Steps that run untrusted commands must be confirmed first.
		if step.RequiresConfirmation {
			prompt := fmt.Sprintf(
				"Restore step %d/%d: run '%s' as part of %s? [y/N] ",
				i+1, len(plan.Steps), step.Detail, step.Label)
			fmt.Fprint(outw, prompt)
			if !userAnswers.Scan() || !yesTo(userAnswers.Text()) {
				fmt.Fprintln(outw, "skipped")
				continue
			}
			fmt.Fprintln(outw, "confirmed")
		}

		if err := p.executeStep(step, outw); err != nil {
			// One failing step must not abort the whole restore.
			fmt.Fprintf(outw, "  ! %v\n", err)
		}
	}

	return nil
}

// yesTo reports whether the user answered "y" or "yes".
func yesTo(answer string) bool {
	a := strings.TrimSpace(strings.ToLower(answer))
	return a == "y" || a == "yes"
}

// pathExists reports whether a non-empty path exists on disk.
func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// gitAvailable reports whether git is on PATH.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// stepLine renders the one-line description printed when a step runs.
func stepLine(step Step) string {
	line := fmt.Sprintf("%s: %s", step.Kind, step.Label)
	if step.Detail != "" {
		line += " — " + step.Detail
	}
	return line
}

// executeStep dispatches a single plan step to its executor.
func (p *Planner) executeStep(step Step, out io.Writer) error {
	switch step.Kind {
	case StepProject:
		return p.executeProject(step, out)
	case StepApplication:
		return p.executeApplication(step, out)
	case StepBrowser:
		return p.executeBrowser(step, out)
	case StepTerminal:
		return p.executeTerminal(step, out)
	case StepService:
		return p.executeService(step, out)
	default:
		return fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

// --- Executors ------------------------------------------------------------

// executeProject reports the project step and clones the project when a
// source is configured and the local path does not exist yet. git is a
// cross-platform tool, so it is invoked directly rather than through an OS
// adapter.
func (p *Planner) executeProject(step Step, out io.Writer) error {
	fmt.Fprintln(out, stepLine(step))

	if step.Status != StatusReady || step.SourceURL == "" {
		return nil
	}
	if step.ProjectPath != "" && pathExists(step.ProjectPath) {
		return nil
	}

	if typ := strings.ToLower(strings.TrimSpace(step.SourceType)); typ != "" && typ != "git" {
		return fmt.Errorf("unsupported project source type %q", step.SourceType)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required to clone the project: %w", platform.ErrNotInstalled)
	}

	args := []string{"clone"}
	if step.SourceBranch != "" {
		args = append(args, "--branch", step.SourceBranch)
	}
	args = append(args, step.SourceURL, step.ProjectPath)

	cmd := exec.Command("git", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone project: %w", err)
	}
	return nil
}

// executeApplication launches a desktop application through the platform
// adapter, opening any paths the workspace recorded.
func (p *Planner) executeApplication(step Step, out io.Writer) error {
	fmt.Fprintln(out, stepLine(step))
	if p.Platform == nil || p.Platform.Apps == nil {
		return fmt.Errorf("application launching is not implemented on this platform")
	}
	return p.Platform.Apps.Launch(step.Label, step.Open...)
}

// executeBrowser opens the browser with its configured tabs.
func (p *Planner) executeBrowser(step Step, out io.Writer) error {
	fmt.Fprintln(out, stepLine(step))
	if p.Platform == nil || p.Platform.Browsers == nil {
		return fmt.Errorf("browser launching is not implemented on this platform")
	}
	return p.Platform.Browsers.Open(step.Label, step.Tabs)
}

// executeTerminal opens a terminal. A non-empty command is untrusted input and
// was confirmed before this point.
func (p *Planner) executeTerminal(step Step, out io.Writer) error {
	fmt.Fprintln(out, stepLine(step))
	if p.Platform == nil || p.Platform.Terminals == nil {
		return fmt.Errorf("terminal launching is not implemented on this platform")
	}
	return p.Platform.Terminals.Open(step.WorkingDirectory, step.Command)
}

// executeService ensures a service is running through the platform service
// manager, unless the plan already decided to skip it.
func (p *Planner) executeService(step Step, out io.Writer) error {
	fmt.Fprintln(out, stepLine(step))
	if step.Status == StatusSkipped {
		return nil
	}
	if p.Platform == nil || p.Platform.Services == nil {
		return fmt.Errorf("service management is not implemented on this platform")
	}
	return p.Platform.Services.Start(step.Label)
}
