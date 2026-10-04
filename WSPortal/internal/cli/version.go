package cli

import (
	"fmt"
	"path/filepath"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

// Version is the WSPortal version string. It can be overridden at build time
// with: go build -ldflags "-X .../internal/cli.Version=..."
var Version = "0.1.0-dev"

func (a *App) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.Out, "wsportal %s\n", Version)
			fmt.Fprintf(a.Out, "schema version: %d\n", workspace.CurrentVersion)
			fmt.Fprintf(a.Out, "platform: %s\n", a.Platform.OS)
			fmt.Fprintf(a.Out, "workspace dir: %s\n", a.Config.WorkspaceDir)
			return nil
		},
	}
}

// workspaceSaveAndReport saves a workspace into the configured directory and
// prints the result. It is shared by commands that produce a workspace.
func workspaceSaveAndReport(a *App, ws *workspace.Workspace, name string) error {
	path := filepath.Join(a.Config.WorkspaceDir, name+workspace.Extension)
	if err := workspace.Save(ws, path); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Wrote %s\n", path)
	return nil
}
