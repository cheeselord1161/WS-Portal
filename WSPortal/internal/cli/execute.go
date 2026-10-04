package cli

import (
	"fmt"
)

// Execute builds the App and runs the command tree against os.Args.
func Execute() error {
	app, err := NewApp()
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	return app.NewRootCommand().Execute()
}

// ExecuteArgs runs the command tree with explicit arguments. It is used by
// tests to drive the CLI without touching os.Args.
func (a *App) ExecuteArgs(args []string) error {
	cmd := a.NewRootCommand()
	cmd.SetArgs(args)
	cmd.SetOut(a.Out)
	cmd.SetErr(a.ErrOut)
	return cmd.Execute()
}
