// Package darwin provides the macOS implementation of the platform
// capabilities.
//
// Applications and browsers are launched through `open`, and terminals that
// need to run a command use AppleScript to drive Terminal.app. Services are
// managed through Homebrew when it is available.
package darwin

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
)

// OS is the GOOS value this adapter targets.
const OS = "darwin"

// Adapter implements the platform capabilities for macOS.
type Adapter struct {
	// Tools is the command-line checker.
	Tools *platform.PathToolChecker
}

// New returns a macOS adapter.
func New() *Adapter {
	return &Adapter{Tools: platform.NewPathToolChecker("open", "osascript", "brew", "sh")}
}

// Bundle returns the adapter as a platform.Adapter.
func (a *Adapter) Bundle() *platform.Adapter {
	return &platform.Adapter{
		OS:        OS,
		Apps:      AppLauncher{a},
		Browsers:  BrowserLauncher{a},
		Terminals: TerminalLauncher{a},
		Services:  ServiceManager{},
		Tools:     a.Tools,
		Processes: ProcessLister{},
	}
}

// launch starts a process without waiting for it to exit.
func launch(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// --- Applications ---------------------------------------------------------

// appNames maps logical application names to their macOS application names.
var appNames = map[string]string{
	"vscode":   "Visual Studio Code",
	"code":     "Visual Studio Code",
	"codium":   "VSCodium",
	"terminal": "Terminal",
	"iterm":    "iTerm",
	"iterm2":   "iTerm",
	"finder":   "Finder",
	"safari":   "Safari",
	"chrome":   "Google Chrome",
	"firefox":  "Firefox",
	"xcode":    "Xcode",
	"sublime":  "Sublime Text",
	"intellij": "IntelliJ IDEA",
	"goland":   "GoLand",
	"pycharm":  "PyCharm",
	"webstorm": "WebStorm",
	"notion":   "Notion",
	"slack":    "Slack",
	"postman":  "Postman",
	"docker":   "Docker",
	"spotify":  "Spotify",
}

// appName resolves the macOS application name for a logical name.
func appName(app string) string {
	if name, ok := appNames[strings.ToLower(strings.TrimSpace(app))]; ok {
		return name
	}
	return strings.TrimSpace(app)
}

// AppLauncher implements platform.AppLauncher.
type AppLauncher struct {
	adapter *Adapter
}

// Launch opens the named application with its arguments via `open -a`.
func (l AppLauncher) Launch(app string, args ...string) error {
	if l.adapter == nil {
		return platform.ErrNotSupported
	}
	name := appName(app)
	if name == "" {
		return fmt.Errorf("application %q: %w", app, platform.ErrNotInstalled)
	}
	return launch("open", append([]string{"-a", name}, args...)...)
}

// Available reports whether the named application exists.
func (l AppLauncher) Available(app string) bool {
	name := appName(app)
	if name == "" {
		return false
	}
	return exec.Command("open", "-Ra", name).Run() == nil
}

// Supported implements platform.Capability.
func (AppLauncher) Supported() bool {
	_, err := exec.LookPath("open")
	return err == nil
}

// Name returns the logical name of this launcher.
func (AppLauncher) Name() string { return "darwin" }

// --- Browsers -------------------------------------------------------------

// BrowserLauncher implements platform.BrowserLauncher.
type BrowserLauncher struct {
	adapter *Adapter
}

// Open opens urls in the named browser, or the default browser when empty.
func (l BrowserLauncher) Open(browser string, urls []string) error {
	if l.adapter == nil || len(urls) == 0 {
		return platform.ErrNotSupported
	}
	key := strings.ToLower(strings.TrimSpace(browser))
	if key == "" || key == "default" {
		return launch("open", urls...)
	}
	return launch("open", append([]string{"-a", appName(key)}, urls...)...)
}

// Supported implements platform.Capability.
func (BrowserLauncher) Supported() bool {
	_, err := exec.LookPath("open")
	return err == nil
}

// Name returns the logical name of this browser launcher.
func (BrowserLauncher) Name() string { return "darwin" }

// --- Terminals ------------------------------------------------------------

// TerminalLauncher implements platform.TerminalLauncher.
type TerminalLauncher struct {
	adapter *Adapter
}

// Open opens Terminal.app, optionally running command in workingDirectory.
func (l TerminalLauncher) Open(workingDirectory, command string) error {
	if l.adapter == nil {
		return platform.ErrNotSupported
	}
	if command == "" {
		args := []string{"-a", "Terminal"}
		if workingDirectory != "" {
			args = append(args, workingDirectory)
		}
		return launch("open", args...)
	}

	cmdline := command
	if workingDirectory != "" {
		cmdline = "cd " + shellEscape(workingDirectory) + " && " + command
	}
	script := fmt.Sprintf("tell application \"Terminal\" to do script %s", appleScriptString(cmdline))
	return launch("osascript", "-e", script)
}

// Supported implements platform.Capability.
func (TerminalLauncher) Supported() bool {
	_, err := exec.LookPath("osascript")
	return err == nil
}

// Name returns the logical name of this terminal launcher.
func (TerminalLauncher) Name() string { return "darwin" }

// shellEscape wraps s in single quotes (POSIX shell safe).
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptString renders s as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// --- Services -------------------------------------------------------------

// ServiceManager implements platform.ServiceManager via Homebrew services.
type ServiceManager struct{}

// Supported implements platform.Capability.
func (ServiceManager) Supported() bool {
	_, err := exec.LookPath("brew")
	return err == nil
}

// IsInstalled reports whether Homebrew knows about the service formula.
func (ServiceManager) IsInstalled(name string) bool {
	if _, err := exec.LookPath("brew"); err != nil {
		return false
	}
	return exec.Command("brew", "list", "--formula", name).Run() == nil
}

// IsRunning reports whether the Homebrew service is started.
func (ServiceManager) IsRunning(name string) bool {
	out, err := exec.Command("brew", "services", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == name && fields[1] == "started" {
			return true
		}
	}
	return false
}

// Start starts the service with `brew services start`.
func (ServiceManager) Start(name string) error {
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("start service %q: %w", name, platform.ErrNotSupported)
	}
	return exec.Command("brew", "services", "start", name).Run()
}

// Name returns the logical name of this service manager.
func (ServiceManager) Name() string { return "brew" }

// --- Processes ------------------------------------------------------------

// ProcessLister implements platform.ProcessLister using `ps`.
type ProcessLister struct{}

// Supported implements platform.ProcessLister.
func (ProcessLister) Supported() bool {
	_, err := exec.LookPath("ps")
	return err == nil
}

// List implements platform.ProcessLister. macOS `ps` does not report the
// working directory without extra tooling, so Cwd is left empty.
func (ProcessLister) List() ([]platform.Process, error) {
	out, err := exec.Command("ps", "-axo", "pid=,comm=,args=").Output()
	if err != nil {
		return nil, err
	}
	var procs []platform.Process
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		p := platform.Process{PID: pid, Name: filepath.Base(fields[1])}
		if len(fields) > 2 {
			p.Args = fields[2:]
		}
		procs = append(procs, p)
	}
	return procs, nil
}
