package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newInspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <workspace>",
		Short: "Display the contents of a workspace file",
		Long: "Read a .ws file and print its contents in a human-readable form.\n\n" +
			"The argument may be a path, a path without the .ws extension, or a\n" +
			"workspace name stored in the WSPortal workspace directory.",
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

			a.printWorkspace(path, ws)
			return nil
		},
	}
}

func (a *App) printWorkspace(path string, ws *workspace.Workspace) {
	w := a.Out

	fmt.Fprintf(w, "Workspace: %s\n", ws.Name())
	if ws.Workspace.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", ws.Workspace.Description)
	}
	fmt.Fprintf(w, "Version: %d\n", ws.Version)
	fmt.Fprintf(w, "File: %s\n", path)

	if ws.Metadata.CreatedBy != "" || ws.Metadata.CreatedAt != "" {
		fmt.Fprintf(w, "Created: %s", ws.Metadata.CreatedBy)
		if ws.Metadata.CreatedAt != "" {
			fmt.Fprintf(w, " at %s", ws.Metadata.CreatedAt)
		}
		fmt.Fprintln(w)
	}

	if p := ws.Project; p != nil {
		fmt.Fprintf(w, "\nProject\n")
		fmt.Fprintf(w, "  name: %s\n", p.Name)
		if p.Path != "" {
			fmt.Fprintf(w, "  path: %s\n", p.Path)
		}
		if p.Source != nil {
			fmt.Fprintf(w, "  source: %s", p.Source.Type)
			if p.Source.URL != "" {
				fmt.Fprintf(w, " %s", p.Source.URL)
			}
			if p.Source.Branch != "" {
				fmt.Fprintf(w, " (%s)", p.Source.Branch)
			}
			fmt.Fprintln(w)
		}
	}

	if len(ws.Applications) > 0 {
		fmt.Fprintf(w, "\nApplications\n")
		for _, app := range ws.Applications {
			label := app.Name
			if label == "" {
				label = app.ID
			}
			if app.ID != "" && app.ID != label {
				fmt.Fprintf(w, "  - %s (id: %s)\n", label, app.ID)
			} else {
				fmt.Fprintf(w, "  - %s\n", label)
			}
			for _, o := range app.Open {
				fmt.Fprintf(w, "      open: %s\n", o)
			}
			if app.WorkingDirectory != "" {
				fmt.Fprintf(w, "      cwd:  %s\n", app.WorkingDirectory)
			}
		}
	}

	if len(ws.Browser) > 0 {
		fmt.Fprintf(w, "\nBrowser\n")
		for _, b := range ws.Browser {
			fmt.Fprintf(w, "  - %s", b.Browser)
			if b.Profile != "" {
				fmt.Fprintf(w, " (profile: %s)", b.Profile)
			}
			fmt.Fprintln(w)
			for wi, win := range b.Windows {
				prefix := "      "
				if len(b.Windows) > 1 {
					fmt.Fprintf(w, "      window %d\n", wi+1)
					prefix = "        "
				}
				for _, tab := range win.Tabs {
					if tab.Title != "" {
						fmt.Fprintf(w, "%s%s — %s\n", prefix, tab.URL, tab.Title)
					} else {
						fmt.Fprintf(w, "%s%s\n", prefix, tab.URL)
					}
				}
			}
		}
	}

	if len(ws.Terminals) > 0 {
		fmt.Fprintf(w, "\nTerminals\n")
		for _, t := range ws.Terminals {
			name := t.Name
			if name == "" {
				name = "terminal"
			}
			fmt.Fprintf(w, "  - %s\n", name)
			if t.WorkingDirectory != "" {
				fmt.Fprintf(w, "      cwd:     %s\n", t.WorkingDirectory)
			}
			if t.Command != "" {
				fmt.Fprintf(w, "      command: %s\n", t.Command)
			}
		}
	}

	if len(ws.Services) > 0 {
		fmt.Fprintf(w, "\nServices\n")
		for _, s := range ws.Services {
			required := "optional"
			if s.Required {
				required = "required"
			}
			fmt.Fprintf(w, "  - %s (%s)", s.Name, required)
			if s.Version != "" {
				fmt.Fprintf(w, ", version %s", s.Version)
			}
			fmt.Fprintln(w)
		}
	}

	if len(ws.Environment.Runtime) > 0 || len(ws.Environment.Tools) > 0 {
		fmt.Fprintf(w, "\nEnvironment\n")
		if len(ws.Environment.Runtime) > 0 {
			names := make([]string, 0, len(ws.Environment.Runtime))
			for name := range ws.Environment.Runtime {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				fmt.Fprintf(w, "  runtime: %s %s\n", name, ws.Environment.Runtime[name])
			}
		}
		if len(ws.Environment.Tools) > 0 {
			fmt.Fprintf(w, "  tools:   %s\n", strings.Join(ws.Environment.Tools, ", "))
		}
	}

	if vars := ws.RequiredVariables(); len(vars) > 0 {
		fmt.Fprintf(w, "\nPortable variables\n")
		for _, v := range vars {
			val, ok := a.Config.Variables[v]
			if ok {
				fmt.Fprintf(w, "  ${%s} = %s\n", v, val)
			} else {
				fmt.Fprintf(w, "  ${%s} (undefined on this machine)\n", v)
			}
		}
	}
}
