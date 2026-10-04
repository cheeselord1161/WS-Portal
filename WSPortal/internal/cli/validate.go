package cli

import (
	"fmt"

	"github.com/cheeselord1161/WS_Portal/internal/validation"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <workspace>",
		Short: "Check whether a workspace file is structurally valid",
		Long: "Validate a .ws file.\n\n" +
			"Validation checks required fields, the schema version, invalid values,\n" +
			"and the overall structure. Errors mean the workspace cannot be used as\n" +
			"written; warnings highlight things worth reviewing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := a.resolveWorkspacePath(args[0])
			if err != nil {
				return err
			}

			ws, err := workspace.Load(path)
			if err != nil {
				return fmt.Errorf("cannot load workspace: %w", err)
			}

			result := a.Validator.Validate(ws)
			a.printValidation(path, result)

			if !result.OK() {
				// A non-zero exit code lets validation be used in scripts.
				return fmt.Errorf("validation failed: %d error(s)", len(result.Errors()))
			}
			return nil
		},
	}
}

func (a *App) printValidation(path string, result validation.Result) {
	w := a.Out
	fmt.Fprintf(w, "Validating %s\n\n", path)

	for _, issue := range result.Errors() {
		fmt.Fprintf(w, "  error:   %s\n", issue)
	}
	for _, issue := range result.Warnings() {
		fmt.Fprintf(w, "  warning: %s\n", issue)
	}

	if result.OK() {
		if len(result.Warnings()) > 0 {
			fmt.Fprintf(w, "\nValid, with %d warning(s).\n", len(result.Warnings()))
		} else {
			fmt.Fprintf(w, "Valid.\n")
		}
		return
	}
	fmt.Fprintf(w, "\nInvalid: %d error(s), %d warning(s).\n",
		len(result.Errors()), len(result.Warnings()))
}
