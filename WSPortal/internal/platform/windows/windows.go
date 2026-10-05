// Package windows provides the Windows implementation of the platform
// capabilities.
//
// It deliberately avoids Windows-only packages (and their build constraints)
// so that it compiles everywhere; instead it drives the standard Windows tools
// `cmd /c start`, `rundll32`, and `sc`. Those binaries only exist on Windows,
// so the adapter is only selected there.
package windows

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
)

// OS is the GOOS value this adapter targets.
const OS = "windows"

// Adapter implements the platform capabilities for Windows.
type Adapter struct {
	// Tools is the command-line checker.
	Tools *platform.PathToolChecker
}

// New returns a Windows adapter.
func New() *Adapter {
	return &Adapter{Tools: platform.NewPathToolChecker("cmd", "rundll32", "sc", "powershell")}
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

// startShell starts a program in the background via the cmd `start` builtin.
// The empty argument is the window title `start` requires.
func startShell(name string, args ...string) error {
	cmdArgs := append([]string{"/c", "start", "", name}, args...)
	cmd := exec.Command("cmd", cmdArgs...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// --- Applications ---------------------------------------------------------

// appAliases maps logical application names to Windows executables.
var appAliases = map[string]string{
	"vscode":   "code",
	"code":     "code",
	"vscodium": "codium",
	"codium":   "codium",
	"cursor":   "cursor",
	"intellij": "idea64",
	"goland":   "goland64",
	"pycharm":  "pycharm64",
	"webstorm": "webstorm64",
	"sublime":  "subl",
	"notepad":  "notepad",
	"explorer": "explorer",
	"terminal": "wt",
	"spotify":  "spotify",
	"slack":    "slack",
	"chrome":   "chrome",
	"chromium": "chrome",
	"firefox":  "firefox",
	"edge":     "msedge",
	"brave":    "brave",
	"vivaldi":  "vivaldi",
	"opera":    "opera",
	"notion":   "notion",
}

// findExecutable locates a Windows executable by name. It checks PATH first,
// then the usual per-user and machine-wide install roots, because applications
// such as Chrome are resolved by ShellExecute (which `start` uses) rather than
// being added to PATH.
func findExecutable(bin string) (string, bool) {
	if p, err := exec.LookPath(bin); err == nil {
		return p, true
	}
	name := bin
	if !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	patterns := []string{
		filepath.Join(name),
		filepath.Join("*", name),
		filepath.Join("*", "*", name),
		filepath.Join("*", "*", "*", name),
	}
	for _, root := range []string{
		os.Getenv("LOCALAPPDATA"),
		os.Getenv("PROGRAMFILES"),
		os.Getenv("PROGRAMFILES(X86)"),
	} {
		if strings.TrimSpace(root) == "" {
			continue
		}
		for _, pattern := range patterns {
			if matches, err := filepath.Glob(filepath.Join(root, pattern)); err == nil && len(matches) > 0 {
				return matches[0], true
			}
		}
	}
	return "", false
}

// appCommand resolves the executable for a logical application name.
func appCommand(app string) string {
	if bin, ok := appAliases[strings.ToLower(strings.TrimSpace(app))]; ok {
		return bin
	}
	return strings.TrimSpace(app)
}

// AppLauncher implements platform.AppLauncher.
type AppLauncher struct {
	adapter *Adapter
}

// Resolve maps a logical application name to its Windows executable. An
// unknown name resolves to a Known=false Application rather than an error.
func (l AppLauncher) Resolve(app string) (platform.Application, error) {
	if l.adapter == nil {
		return platform.Application{}, platform.ErrNotSupported
	}
	known, ok := platform.LookupApplication(app)
	if !ok {
		id := platform.CanonicalAppID(app)
		return platform.Application{ID: id, Name: id}, nil
	}
	res := platform.Application{ID: known.ID, Name: known.Name, Known: true}
	if bin := appCommand(known.ID); bin != "" {
		if path, found := findExecutable(bin); found {
			res.Executable = path
			res.Installed = true
		}
	}
	return res, nil
}

// Launch opens the named application with its arguments. Only a resolved,
// installed application is launched; an unknown id is never executed.
func (l AppLauncher) Launch(app string, args ...string) error {
	res, err := l.Resolve(app)
	if err != nil {
		return err
	}
	if !res.Known || !res.Installed {
		return fmt.Errorf("application %q: %w", app, platform.ErrNotInstalled)
	}
	return startShell(res.Executable, args...)
}

// Supported implements platform.Capability.
func (AppLauncher) Supported() bool {
	_, err := exec.LookPath("cmd")
	return err == nil
}

// Name returns the logical name of this launcher.
func (AppLauncher) Name() string { return "windows" }

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
		for _, u := range urls {
			if err := startRundll(u); err != nil {
				return err
			}
		}
		return nil
	}
	return startShell(appCommand(key), urls...)
}

// startRundll opens a URL with the registered protocol handler.
func startRundll(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Supported implements platform.Capability.
func (BrowserLauncher) Supported() bool {
	_, err := exec.LookPath("cmd")
	return err == nil
}

// Name returns the logical name of this browser launcher.
func (BrowserLauncher) Name() string { return "windows" }

// --- Terminals ------------------------------------------------------------

// TerminalLauncher implements platform.TerminalLauncher.
type TerminalLauncher struct {
	adapter *Adapter
}

// Open opens a cmd window, optionally running command in workingDirectory.
func (l TerminalLauncher) Open(workingDirectory, command string) error {
	if l.adapter == nil {
		return platform.ErrNotSupported
	}
	var parts []string
	if workingDirectory != "" {
		parts = append(parts, "cd /d "+quote(workingDirectory))
	}
	if command != "" {
		parts = append(parts, command)
	}
	inner := strings.Join(parts, " && ")
	if inner == "" {
		inner = "cmd"
	}
	// `start "" cmd /k <inner>` keeps the window open after the command.
	return startShell("cmd", "/k", inner)
}

// quote wraps s in double quotes if it contains spaces.
func quote(s string) string {
	if strings.ContainsAny(s, " \t") && !strings.HasPrefix(s, `"`) {
		return `"` + s + `"`
	}
	return s
}

// Supported implements platform.Capability.
func (TerminalLauncher) Supported() bool {
	_, err := exec.LookPath("cmd")
	return err == nil
}

// Name returns the logical name of this terminal launcher.
func (TerminalLauncher) Name() string { return "windows" }

// --- Services -------------------------------------------------------------

// ServiceManager implements platform.ServiceManager via the `sc` tool.
type ServiceManager struct{}

// Supported implements platform.Capability.
func (ServiceManager) Supported() bool {
	_, err := exec.LookPath("sc")
	return err == nil
}

// IsInstalled reports whether the named Windows service exists.
func (ServiceManager) IsInstalled(name string) bool {
	if _, err := exec.LookPath("sc"); err != nil {
		return false
	}
	return exec.Command("sc", "query", name).Run() == nil
}

// IsRunning reports whether the service state is RUNNING.
func (ServiceManager) IsRunning(name string) bool {
	out, err := exec.Command("sc", "query", name).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "RUNNING")
}

// Start attempts to start a Windows service.
func (ServiceManager) Start(name string) error {
	if _, err := exec.LookPath("sc"); err != nil {
		return fmt.Errorf("start service %q: %w", name, platform.ErrNotSupported)
	}
	return exec.Command("sc", "start", name).Run()
}

// Name returns the logical name of this service manager.
func (ServiceManager) Name() string { return "sc" }

// --- Processes ------------------------------------------------------------

// ProcessLister implements platform.ProcessLister using PowerShell.
type ProcessLister struct{}

// Supported implements platform.ProcessLister.
func (ProcessLister) Supported() bool {
	_, err := exec.LookPath(powershell())
	return err == nil
}

// powershell returns the PowerShell executable to use.
func powershell() string {
	if _, err := exec.LookPath("powershell"); err == nil {
		return "powershell"
	}
	return "pwsh"
}

// List implements platform.ProcessLister.
func (ProcessLister) List() ([]platform.Process, error) {
	out, err := exec.Command(powershell(), "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,Name,CommandLine | ConvertTo-Csv -NoTypeInformation",
	).Output()
	if err != nil {
		return nil, err
	}

	records, err := csv.NewReader(bytes.NewReader(out)).ReadAll()
	if err != nil {
		return nil, err
	}

	procs := make([]platform.Process, 0, len(records))
	for i, rec := range records {
		if i == 0 || len(rec) < 2 { // skip the CSV header
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(rec[0]))
		if err != nil {
			continue
		}
		p := platform.Process{PID: pid, Name: strings.TrimSpace(rec[1])}
		if len(rec) > 2 && strings.TrimSpace(rec[2]) != "" {
			p.Args = strings.Fields(rec[2])
		}
		procs = append(procs, p)
	}
	return procs, nil
}
