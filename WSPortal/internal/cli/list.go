package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List locally available workspaces",
		Long: "List the workspace files stored in the WSPortal workspace directory.\n\n" +
			"Workspaces stored elsewhere are not listed; pass their path directly to\n" +
			"commands such as `ws inspect` or `ws resume`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := a.listWorkspaceFiles()
			if err != nil {
				return err
			}

			if len(files) == 0 {
				fmt.Fprintf(a.Out, "No workspaces found in %s\n", a.Config.WorkspaceDir)
				fmt.Fprintf(a.Out, "\nCreate one with:\n  ws init <name>\n")
				return nil
			}

			fmt.Fprintf(a.Out, "Available workspaces:\n\n")
			for _, f := range files {
				name := strings.TrimSuffix(filepath.Base(f), workspace.Extension)
				fmt.Fprintf(a.Out, "  %s\n", name)
			}
			return nil
		},
	}
}
