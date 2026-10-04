// Package cli wires the WSPortal commands into a Cobra command tree.
//
// Commands depend on an App value rather than on package-level globals. That
// keeps them easy to test: a test can construct an App with buffers for output
// and a temporary workspace directory.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/config"
	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/platform/detect"
	"github.com/cheeselord1161/WS_Portal/internal/validation"
	"github.com/spf13/cobra"
)

// App holds the dependencies shared by all commands.
type App struct {
	// Config is the resolved runtime configuration.
	Config *config.Config
	// Platform is the adapter for the current operating system.
	Platform *platform.Adapter
	// Validator validates workspaces.
	Validator *validation.Validator
	// Out is where normal command output is written.
	Out io.Writer
	// ErrOut is where error and diagnostic output is written.
	ErrOut io.Writer
}

// NewApp constructs an App with production defaults.
func NewApp() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return &App{
		Config:    cfg,
		Platform:  detect.Current(),
		Validator: validation.New(),
		Out:       os.Stdout,
		ErrOut:    os.Stderr,
	}, nil
}

// NewRootCommand builds the `ws` command tree.
func (a *App) NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "ws",
		Short: "WSPortal — portable workspaces, anywhere",
		Long: "WSPortal makes your working environment portable.\n\n" +
			"Save the environment you work in once, then restore it on another\n" +
			"computer with a single command. A workspace (.ws) file describes the\n" +
			"environment you want — project, applications, browser tabs, terminals,\n" +
			"services, and dependencies — rather than recording what you did.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		a.newInitCommand(),
		a.newCaptureCommand(),
		a.newInspectCommand(),
		a.newValidateCommand(),
		a.newResumeCommand(),
		a.newExportCommand(),
		a.newImportCommand(),
		a.newSendCommand(),
		a.newReceiveCommand(),
		a.newListCommand(),
		a.newVersionCommand(),
		a.newDoctorCommand(),
	)

	return root
}

func (a *App) newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the local environment for workspace restore dependencies",
		Long: "Inspect the local machine and report what WSPortal can and cannot " +
			"restore yet. Each finding is a human-readable line (for example " +
			"✓ Git, ✗ PostgreSQL not installed). Exits 0 when every checked " +
			"dependency is present; exits 1 when at least one required dependency " +
			"is missing.\n" +
			"\n" +
			"Doctor does not make any changes to your machine.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Platform == nil {
				fmt.Fprintln(a.Out, "platform: unknown")
			}

			var missing []string

			if a.Platform != nil && a.Platform.Tools != nil {
				for _, tool := range a.Platform.Tools.Ordered() {
					if _, ok := a.Platform.Tools.LookPath(tool); ok {
						fmt.Fprintf(a.Out, "✓ %s\n", tool)
					} else {
						fmt.Fprintf(a.Out, "✗ %s (not installed)\n", tool)
						missing = append(missing, tool)
					}
				}
			}

			if p := a.Platform; p != nil {
				if p.Apps != nil {
					for _, name := range []string{"vscode", "code"} {
						if p.Apps.Available(name) {
							fmt.Fprintf(a.Out, "✓ %s\n", name)
						}
					}
				}
			}

			if len(missing) > 0 {
				fmt.Fprintf(a.Out, "\n%d warning(s) found.\n", len(missing))
				return fmt.Errorf("missing tools: %s", strings.Join(missing, ", "))
			}
			fmt.Fprintf(a.Out, "\nNo missing dependencies found.\n")
			return nil
		},
	}
}
