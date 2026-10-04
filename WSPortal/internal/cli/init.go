package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newInitCommand() *cobra.Command {
	var (
		description string
		dir         string
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Create a new workspace definition",
		Long: "Create a new .ws workspace file.\n\n" +
			"By default the file is written to the WSPortal workspace directory\n" +
			"as <name>.ws. Use --dir to write it somewhere else, for example into\n" +
			"the project you are describing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("workspace name cannot be empty")
			}

			targetDir := dir
			if targetDir == "" {
				targetDir = a.Config.WorkspaceDir
			}
			if targetDir == "" {
				return fmt.Errorf("no workspace directory configured; pass --dir")
			}
			if err := os.MkdirAll(targetDir, 0o755); err != nil {
				return fmt.Errorf("create workspace directory: %w", err)
			}

			path := filepath.Join(targetDir, name+workspace.Extension)
			if !force {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", path)
				}
			}

			ws := workspace.New(name, description)
			if err := workspace.Save(ws, path); err != nil {
				return err
			}

			fmt.Fprintf(a.Out, "Created workspace %q\n", name)
			fmt.Fprintf(a.Out, "  file: %s\n", path)
			fmt.Fprintf(a.Out, "\nNext steps:\n")
			fmt.Fprintf(a.Out, "  ws inspect %s   # view the definition\n", path)
			fmt.Fprintf(a.Out, "  ws validate %s  # check it\n", path)
			fmt.Fprintf(a.Out, "  ws resume %s    # preview restoring it\n", path)
			return nil
		},
	}

	cmd.Flags().StringVar(&description, "description", "", "human-readable description of the workspace")
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "directory to write the workspace file into")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing workspace file")

	return cmd
}
