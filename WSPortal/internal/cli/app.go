// Package cli wires the WSPortal commands into a Cobra command tree.
//
// Commands depend on an App value rather than on package-level globals. That
// keeps them easy to test: a test can construct an App with buffers for output
// and a temporary workspace directory.
package cli

import (
	"io"
	"os"

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
	)

	return root
}
