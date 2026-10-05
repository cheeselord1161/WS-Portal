package cli

import (
	"fmt"
	"os"

	"github.com/cheeselord1161/WS_Portal/internal/restore"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newResumeCommand() *cobra.Command {
	var (
		execute     bool
		installDeps bool
	)

	cmd := &cobra.Command{
		Use:   "resume <workspace>",
		Short: "Restore a working environment",
		Long: "Reconstruct the environment described by a workspace.\n\n" +
			"By default this previews the restore plan without changing your machine.\n" +
			"Pass --execute to launch applications, open browser tabs, open terminals,\n" +
			"clone the project, and start services. Pass --install-deps to install the\n" +
			"tools the workspace declares first.\n\n" +
			"Commands from imported workspaces, dependency installs, and project\n" +
			"clones are never run without confirmation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.resolveWorkspacePath(args[0])
			if err != nil {
				return err
			}

			ws, err := workspace.Load(path)
			if err != nil {
				return err
			}

			// Validate before planning, as the design requires.
			result := a.Validator.Validate(ws)
			if !result.OK() {
				a.printValidation(path, result)
				return fmt.Errorf("workspace is invalid; run `ws validate %s` for details", path)
			}

			// Build the plan through the restore engine interface so a real
			// engine can replace this planner later without changing the CLI.
			engine := restore.NewPlanner(a.Platform, a.Config.Resolver())

			plan, err := engine.Plan(ws)
			if err != nil {
				return err
			}

			a.printPlan(plan)

			if !execute && !installDeps {
				fmt.Fprintf(a.Out, "\nThis is a preview. Run `ws resume %s --execute` to apply it.\n", args[0])
				return nil
			}

			// Confirmation prompts read answers line by line from stdin so the
			// user can approve or skip each untrusted action interactively.
			scanner := restore.NewReaderScanner(os.Stdin)

			if installDeps {
				goos := ""
				if a.Platform != nil {
					goos = a.Platform.OS
				}
				installer := restore.NewInstaller(goos, ws.Environment.Tools)
				if err := installer.Install(a.Out, scanner); err != nil {
					return err
				}
			}

			if !execute {
				fmt.Fprintf(a.Out, "\nDependency installation finished. Run `ws resume %s --execute` to apply the plan.\n", args[0])
				return nil
			}

			if err := engine.Execute(plan, a.Out, scanner); err != nil {
				return err
			}

			fmt.Fprintf(a.Out, "\nRestore complete.\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&execute, "execute", false, "actually perform the restore")
	cmd.Flags().BoolVar(&installDeps, "install-deps", false, "install missing declared tools, with confirmation")

	return cmd
}

// stepMark renders the status glyph used in the restore plan. A missing
// application or dependency is a warning (it does not stop the restore), so it
// is drawn with the warning mark rather than a hard failure mark.
func stepMark(status restore.StepStatus) string {
	switch status {
	case restore.StatusReady:
		return "✓"
	case restore.StatusMissing:
		return "⚠"
	case restore.StatusSkipped:
		return "-"
	case restore.StatusUnsupported:
		return "~"
	default:
		return "?"
	}
}

func (a *App) printPlan(plan *restore.Plan) {
	w := a.Out

	fmt.Fprintf(w, "Workspace: %s\n", plan.WorkspaceName)
	if plan.Platform != "" {
		fmt.Fprintf(w, "Platform:  %s\n", plan.Platform)
	}
	fmt.Fprintf(w, "\nRestore plan:\n\n")

	if len(plan.Steps) == 0 {
		fmt.Fprintf(w, "\n  (nothing to restore)\n")
	}

	// Group the plan by category so it reads as a checklist rather than a
	// flat dump. Steps are already in execution order within each category.
	order := []restore.StepKind{
		restore.StepProject, restore.StepApplication, restore.StepBrowser,
		restore.StepTerminal, restore.StepService,
	}
	headers := map[restore.StepKind]string{
		restore.StepProject:     "Project",
		restore.StepApplication: "Applications",
		restore.StepBrowser:     "Browser",
		restore.StepTerminal:    "Terminals",
		restore.StepService:     "Services",
	}
	grouped := make(map[restore.StepKind][]restore.Step)
	for _, step := range plan.Steps {
		grouped[step.Kind] = append(grouped[step.Kind], step)
	}

	for _, kind := range order {
		steps := grouped[kind]
		if len(steps) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s:\n", headers[kind])
		for _, step := range steps {
			fmt.Fprintf(w, "  %s %s", stepMark(step.Status), step.Label)
			if step.Detail != "" {
				fmt.Fprintf(w, " — %s", step.Detail)
			}
			if step.RequiresConfirmation {
				fmt.Fprintf(w, " (requires confirmation)")
			}
			if step.Reason != "" {
				fmt.Fprintf(w, "\n      %s", step.Reason)
			}
			fmt.Fprintln(w)
		}
	}

	if len(plan.Dependencies) > 0 {
		fmt.Fprintf(w, "\nDependencies:\n")
		for _, dep := range plan.Dependencies {
			fmt.Fprintln(w, dep)
		}
	}

	if len(plan.Warnings) > 0 {
		fmt.Fprintf(w, "\nWarnings:\n")
		for _, warning := range plan.Warnings {
			fmt.Fprintf(w, "  ! %s\n", warning)
		}
	}
}
