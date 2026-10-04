package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cheeselord1161/WS_Portal/internal/transfer"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
	"github.com/spf13/cobra"
)

func (a *App) newExportCommand() *cobra.Command {
	var out string

	cmd := &cobra.Command{
		Use:   "export <workspace>",
		Short: "Copy a workspace file to a shareable location",
		Long: "Export a workspace as a plain .ws file.\n\n" +
			"Exporting needs no account and no network connection: the workspace is\n" +
			"a portable YAML file that can be emailed, committed, or copied to a\n" +
			"USB stick. Keep in mind that a workspace may describe commands and\n" +
			"paths; review it before sharing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := a.resolveWorkspacePath(args[0])
			if err != nil {
				return err
			}

			ws, err := workspace.Load(src)
			if err != nil {
				return err
			}

			target := out
			if target == "" {
				target = filepath.Base(src)
			}
			if err := workspace.Save(ws, target); err != nil {
				return err
			}

			fmt.Fprintf(a.Out, "Exported workspace %q to %s\n", ws.Name(), target)
			return nil
		},
	}

	cmd.Flags().StringVarP(&out, "out", "o", "", "destination file (default: current directory)")

	return cmd
}

func (a *App) newImportCommand() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import a workspace file",
		Long: "Import a .ws file into the WSPortal workspace directory.\n\n" +
			"Imported workspaces are treated as untrusted: they may contain commands\n" +
			"and URLs. WSPortal validates the file on import and never runs commands\n" +
			"from it without asking you first.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if !workspace.HasExtension(path) {
				path += workspace.Extension
			}

			ws, err := workspace.Load(path)
			if err != nil {
				return err
			}

			result := a.Validator.Validate(ws)
			if !result.OK() {
				a.printValidation(path, result)
				return fmt.Errorf("refusing to import an invalid workspace")
			}

			target := strings.TrimSpace(name)
			if target == "" {
				target = ws.Name()
			}
			if target == "" {
				target = strings.TrimSuffix(filepath.Base(path), workspace.Extension)
			}

			dest := filepath.Join(a.Config.WorkspaceDir, target+workspace.Extension)
			if err := os.MkdirAll(a.Config.WorkspaceDir, 0o755); err != nil {
				return fmt.Errorf("create workspace directory: %w", err)
			}
			if err := workspace.Save(ws, dest); err != nil {
				return err
			}

			fmt.Fprintf(a.Out, "Imported workspace %q to %s\n", ws.Name(), dest)
			if len(result.Warnings()) > 0 {
				fmt.Fprintf(a.Out, "%d warning(s) — run `ws validate %s` to review.\n", len(result.Warnings()), dest)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "store the workspace under a different name")

	return cmd
}

func (a *App) newSendCommand() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "send <workspace>",
		Short: "Send a workspace to another machine (LAN)",
		Long: "Send a workspace directly to another computer over the local network.\n\n" +
			"Run `ws send <workspace>` on this machine and `ws receive` on the other\n" +
			"one; they find each other automatically. The workspace travels in the\n" +
			"clear on your LAN and by policy contains no secrets, so only send it on\n" +
			"a network you trust.",
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

			fmt.Fprintf(a.Out, "Waiting for a receiver on the local network (Ctrl-C to cancel)...\n")
			sender := transfer.LANSender{Timeout: timeout}
			receipt, err := sender.Send(ws)
			if err != nil {
				return err
			}

			fmt.Fprintf(a.Out, "Sent workspace %q.\n", ws.Name())
			fmt.Fprintf(a.Out, "Transfer code: %s\n", receipt.Code)
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "how long to wait for a receiver")

	return cmd
}

func (a *App) newReceiveCommand() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "receive",
		Short: "Receive a workspace from another machine (LAN)",
		Long: "Receive a workspace sent from another computer over the local network.\n\n" +
			"Run `ws receive` and it waits for a `ws send` on the same network.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.Out, "Waiting for a sender on the local network (Ctrl-C to cancel)...\n")
			receiver := transfer.LANReceiver{Timeout: timeout}
			ws, err := receiver.Receive("")
			if err != nil {
				return err
			}

			name := ws.Name()
			if name == "" {
				name = "received"
			}
			if err := workspaceSaveAndReport(a, ws, name); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "Received workspace %q.\n", name)
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "how long to wait for a sender")

	return cmd
}
