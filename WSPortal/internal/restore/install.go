package restore

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// InstallStep describes one dependency installation command.
type InstallStep struct {
	// Name is the tool being installed.
	Name string
	// Package is the package name passed to the package manager.
	Package string
	// Command is the full command line to run.
	Command []string
}

// Installer detects missing tools and installs them with the platform's
// package manager. Every command is confirmed before it runs. Installation is
// strictly opt-in: the CLI only builds an Installer when --install-deps is set.
type Installer struct {
	// GOOS is the target operating system (runtime.GOOS values).
	GOOS string
	// Tools are the tool names to ensure are installed.
	Tools []string
	// LookPath reports a binary's path and whether it was found.
	LookPath func(string) (string, bool)
	// Run executes a command, streaming output. It is injectable for tests.
	Run func(args []string, out io.Writer) error
	// Sudo prefixes commands with sudo when the process is not root.
	Sudo bool
}

// NewInstaller builds an Installer for the given OS and tool list.
func NewInstaller(goos string, tools []string) *Installer {
	i := &Installer{
		GOOS:  goos,
		Tools: tools,
		LookPath: func(name string) (string, bool) {
			path, err := exec.LookPath(name)
			if err != nil {
				return "", false
			}
			return path, true
		},
		Run: func(args []string, out io.Writer) error {
			if len(args) == 0 {
				return fmt.Errorf("empty install command")
			}
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Stdout = out
			cmd.Stderr = out
			return cmd.Run()
		},
	}
	if goos != "windows" && os.Geteuid() != 0 {
		if _, ok := i.LookPath("sudo"); ok {
			i.Sudo = true
		}
	}
	return i
}

// Missing returns the declared tools that are not on PATH.
func (i *Installer) Missing() []string {
	var out []string
	for _, tool := range i.Tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := i.LookPath(tool); !ok {
			out = append(out, tool)
		}
	}
	return out
}

// packageManager describes how to install a package on this machine.
type packageManager struct {
	bin     string
	install func(pkg string) []string
}

// manager returns the package manager to use, or nil when none is available.
func (i *Installer) manager() *packageManager {
	switch i.GOOS {
	case "windows":
		if _, ok := i.LookPath("winget"); ok {
			return &packageManager{"winget", func(pkg string) []string {
				return []string{"winget", "install", "--silent",
					"--accept-package-agreements", "--accept-source-agreements", pkg}
			}}
		}
		if _, ok := i.LookPath("choco"); ok {
			return &packageManager{"choco", func(pkg string) []string {
				return []string{"choco", "install", "-y", pkg}
			}}
		}
	default:
		candidates := []*packageManager{
			{"apt-get", func(pkg string) []string { return []string{"apt-get", "install", "-y", pkg} }},
			{"dnf", func(pkg string) []string { return []string{"dnf", "install", "-y", pkg} }},
			{"zypper", func(pkg string) []string { return []string{"zypper", "--non-interactive", "install", pkg} }},
			{"pacman", func(pkg string) []string { return []string{"pacman", "-S", "--noconfirm", pkg} }},
			{"apk", func(pkg string) []string { return []string{"apk", "add", pkg} }},
		}
		for _, candidate := range candidates {
			if _, ok := i.LookPath(candidate.bin); ok {
				return candidate
			}
		}
	}
	return nil
}

// aptPackages maps a tool name to its Debian/Ubuntu package name when they
// differ.
var aptPackages = map[string]string{
	"node":   "nodejs",
	"go":     "golang",
	"docker": "docker.io",
	"python": "python3",
	"pip":    "python3-pip",
	"gcc":    "build-essential",
	"java":   "default-jdk",
}

// pacmanPackages maps a tool name to its Arch package name when they differ.
var pacmanPackages = map[string]string{
	"node": "nodejs",
	"go":   "go",
}

// packageName resolves the package name for a tool on the given manager.
func packageName(manager, goos, tool string) string {
	if goos == "linux" {
		switch manager {
		case "apt-get":
			if pkg, ok := aptPackages[tool]; ok {
				return pkg
			}
		case "pacman":
			if pkg, ok := pacmanPackages[tool]; ok {
				return pkg
			}
		}
	}
	return tool
}

// Steps returns the installation commands for the missing tools, or nil when
// no package manager is available or nothing is missing.
func (i *Installer) Steps() []InstallStep {
	mgr := i.manager()
	if mgr == nil {
		return nil
	}
	var steps []InstallStep
	for _, tool := range i.Missing() {
		pkg := packageName(mgr.bin, i.GOOS, tool)
		command := mgr.install(pkg)
		if i.Sudo {
			command = append([]string{"sudo"}, command...)
		}
		steps = append(steps, InstallStep{Name: tool, Package: pkg, Command: command})
	}
	return steps
}

// Install prompts for and runs each installation step. Unconfirmed steps are
// skipped. A failed install is reported but does not stop the remaining ones.
func (i *Installer) Install(out io.Writer, answers *Scanner) error {
	if out == nil {
		out = io.Discard
	}
	if answers == nil {
		return ErrConfirmation
	}

	steps := i.Steps()
	if len(steps) == 0 {
		if mgr := i.manager(); mgr == nil && len(i.Missing()) > 0 {
			fmt.Fprintln(out, "No supported package manager found; install the missing tools manually.")
		}
		return nil
	}

	for _, step := range steps {
		fmt.Fprintf(out, "Install %s with `%s`? [y/N] ", step.Name, strings.Join(step.Command, " "))
		if !answers.Scan() || !yesTo(answers.Text()) {
			fmt.Fprintln(out, "skipped")
			continue
		}
		if err := i.Run(step.Command, out); err != nil {
			fmt.Fprintf(out, "  ! installing %s: %v\n", step.Name, err)
			continue
		}
		fmt.Fprintf(out, "  installed %s\n", step.Name)
	}
	return nil
}
