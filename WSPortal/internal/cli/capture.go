package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cheeselord1161/WS_Portal/internal/capture"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newCaptureCommand() *cobra.Command {
	var (
		noEditors  bool
		noBrowsers bool
		overwrite  bool
	)

	cmd := &cobra.Command{
		Use:   "capture <name>",
		Short: "Capture the current working environment",
		Long: "Observe the current working environment and write a workspace that\n" +
			"describes it.\n\n" +
			"Capture records meaningful context — the project you have open, the\n" +
			"applications and browsers you are running, the directories your\n" +
			"terminals are in, the services that are up, and the tools installed.\n\n" +
			"It also reads the applications' own state to recover more detail: the\n" +
			"folders open in VS Code and the recent projects in JetBrains IDEs, and\n" +
			"the tabs open in Chromium-based and Firefox browsers. Use --no-editors\n" +
			"or --no-browsers to leave those out. It never records keystrokes, mouse\n" +
			"movement, screen contents, or command lines.\n\n" +
			"If a workspace with the same name already exists, the captured\n" +
			"environment is merged into it rather than replacing it, so content you\n" +
			"added by hand is preserved. Pass --overwrite to replace it instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			engine := capture.NewProcessEngine(a.Platform)
			if noEditors {
				engine.DisableSources("editors")
			}
			if noBrowsers {
				engine.DisableSources("browsers")
			}

			ws, err := engine.Capture(name)
			if err != nil {
				if errors.Is(err, capture.ErrNotImplemented) {
					fmt.Fprintf(a.Out, "ws capture %s\n\n", name)
					fmt.Fprintf(a.Out, "Automatic capture is not supported on this platform.\n\n")
					fmt.Fprintf(a.Out, "Create a workspace and describe it by hand instead:\n")
					fmt.Fprintf(a.Out, "  ws init %s\n", name)
					return nil
				}
				return err
			}

			if _, err := a.saveCaptured(ws, name, overwrite); err != nil {
				return err
			}

			tabs := 0
			for _, session := range ws.Browser {
				tabs += len(session.TabURLs())
			}

			fmt.Fprintf(a.Out, "Captured from the running environment:\n")
			fmt.Fprintf(a.Out, "  project:      %s\n", projectSummary(ws.Project))
			fmt.Fprintf(a.Out, "  applications: %d\n", len(ws.Applications))
			fmt.Fprintf(a.Out, "  browsers:     %d (%d tab(s))\n", len(ws.Browser), tabs)
			fmt.Fprintf(a.Out, "  terminals:    %d\n", len(ws.Terminals))
			fmt.Fprintf(a.Out, "  services:     %d\n", len(ws.Services))
			fmt.Fprintf(a.Out, "  tools:        %d\n", len(ws.Environment.Tools))
			fmt.Fprintf(a.Out, "\nReview it with `ws inspect %s` before relying on it.\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&noEditors, "no-editors", false, "do not read open editor workspaces")
	cmd.Flags().BoolVar(&noBrowsers, "no-browsers", false, "do not read open browser tabs")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing workspace instead of merging into it")

	return cmd
}

// saveCaptured writes a captured workspace. When a file of the same name
// already exists it is merged with the capture, unless overwrite is set, so a
// re-capture never discards hand-written content by accident. It reports
// whether an existing workspace was merged into.
func (a *App) saveCaptured(ws *workspace.Workspace, name string, overwrite bool) (bool, error) {
	path := filepath.Join(a.Config.WorkspaceDir, name+workspace.Extension)

	merged := false
	if !overwrite && fileExists(path) {
		existing, err := workspace.Load(path)
		if err != nil {
			return false, fmt.Errorf("merge with existing workspace %s: %w", path, err)
		}
		ws = workspace.Merge(existing, ws)
		merged = true
	}

	if err := workspace.Save(ws, path); err != nil {
		return false, err
	}
	if merged {
		fmt.Fprintf(a.Out, "Updated %s (merged with the existing workspace)\n", path)
	} else {
		fmt.Fprintf(a.Out, "Wrote %s\n", path)
	}
	return merged, nil
}

// projectSummary renders a project block for the capture summary.
func projectSummary(p *workspace.Project) string {
	if p == nil {
		return "(none detected)"
	}
	name := p.Name
	if name == "" {
		name = "project"
	}
	if p.Path != "" {
		return name + " (" + p.Path + ")"
	}
	return name
}
