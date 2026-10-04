// Package linux provides the Linux implementation of the platform
// capabilities.
//
// It uses standard, desktop-agnostic tools so the adapter works on GNOME,
// KDE, XFCE, and even headless servers:
//
//	Applications	the mapped executable, then gtk-launch (XDG desktop entry)
//	Browser tabs	the browser executable, or xdg-open for the default browser
//	Terminals	a detected emulator (gnome-terminal, konsole, xterm, ...)
//	Services	systemctl (systemd)
//
// Long-running launches are backgrounded so a restore does not block on the
// application staying open.
package linux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
)

// OS is the GOOS value this adapter targets.
const OS = "linux"

// Adapter implements the platform capabilities for Linux.
type Adapter struct {
	// Tools is the command-line checker. If nil the adapter reports every tool
	// as missing.
	Tools *platform.PathToolChecker

	// terminalEmulator is the executable used to open a terminal. It is
	// detected at construction time and may be empty on a headless machine.
	terminalEmulator string
}

// New returns a Linux adapter.
func New() *Adapter {
	t := platform.NewPathToolChecker(
		"xdg-open", "gtk-launch", "systemctl", "sh", "bash",
	)
	return &Adapter{Tools: t, terminalEmulator: detectTerminal()}
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

// launch starts a process without waiting for it to finish, so the restore can
// carry on while the application stays open.
func launch(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// --- Applications ---------------------------------------------------------

// appAliases maps logical application names to the executables that provide
// them on Linux, in order of preference.
var appAliases = map[string][]string{
	"vscode":   {"code", "code-oss", "codium"},
	"code":     {"code", "code-oss", "codium"},
	"vscodium": {"codium"},
	"intellij": {"idea"},
	"idea":     {"idea"},
	"goland":   {"goland"},
	"pycharm":  {"pycharm"},
	"webstorm": {"webstorm"},
	"sublime":  {"subl", "sublime_text"},
	"atom":     {"atom"},
	"neovim":   {"nvim"},
	"vim":      {"vim"},
	"emacs":    {"emacs"},
	"gimp":     {"gimp"},
	"inkscape": {"inkscape"},
	"blender":  {"blender"},
	"postman":  {"postman"},
	"dbeaver":  {"dbeaver"},
}

// AppLauncher implements platform.AppLauncher. It opens a desktop application.
type AppLauncher struct {
	adapter *Adapter
}

// appCommand resolves the executable for a logical application name.
func (a *Adapter) appCommand(app string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(app))
	if key == "" {
		return "", false
	}
	for _, bin := range appAliases[key] {
		if _, err := exec.LookPath(bin); err == nil {
			return bin, true
		}
	}
	if _, err := exec.LookPath(key); err == nil {
		return key, true
	}
	return "", false
}

// Launch opens the desktop application with its arguments.
func (l AppLauncher) Launch(app string, args ...string) error {
	if l.adapter == nil {
		return platform.ErrNotSupported
	}
	if bin, ok := l.adapter.appCommand(app); ok {
		return launch(bin, args...)
	}
	// Fall back to the XDG desktop entry launcher, which understands the
	// application's registered name.
	if _, err := exec.LookPath("gtk-launch"); err == nil {
		return launch("gtk-launch", append([]string{app}, args...)...)
	}
	return fmt.Errorf("application %q: %w", app, platform.ErrNotInstalled)
}

// Available reports whether the named application appears to be installed.
func (l AppLauncher) Available(app string) bool {
	if l.adapter == nil {
		return false
	}
	_, ok := l.adapter.appCommand(app)
	return ok
}

// Supported implements platform.Capability.
func (AppLauncher) Supported() bool { return true }

// Name returns the logical name of this launcher.
func (AppLauncher) Name() string { return "linux" }

// --- Browsers -------------------------------------------------------------

// browserAliases maps logical browser names to their executables.
var browserAliases = map[string][]string{
	"chrome":        {"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"},
	"google-chrome": {"google-chrome", "google-chrome-stable", "chromium"},
	"chromium":      {"chromium", "chromium-browser"},
	"firefox":       {"firefox"},
	"brave":         {"brave-browser", "brave"},
	"edge":          {"microsoft-edge", "microsoft-edge-stable"},
	"vivaldi":       {"vivaldi"},
	"opera":         {"opera"},
}

// BrowserLauncher implements platform.BrowserLauncher.
type BrowserLauncher struct {
	adapter *Adapter
}

// Open opens urls in the named browser. An empty or "default" browser name
// opens the system default browser.
func (l BrowserLauncher) Open(browser string, urls []string) error {
	if l.adapter == nil || len(urls) == 0 {
		return platform.ErrNotSupported
	}
	key := strings.ToLower(strings.TrimSpace(browser))
	if key == "" || key == "default" {
		return openDefault(urls)
	}

	var bin string
	for _, cand := range browserAliases[key] {
		if _, err := exec.LookPath(cand); err == nil {
			bin = cand
			break
		}
	}
	if bin == "" {
		if _, err := exec.LookPath(key); err == nil {
			bin = key
		}
	}
	if bin == "" {
		// Unknown browser: fall back to the system default rather than failing.
		return openDefault(urls)
	}
	return launch(bin, urls...)
}

// openDefault opens each URL in the system default browser.
func openDefault(urls []string) error {
	for _, u := range urls {
		if err := launch("xdg-open", u); err != nil {
			return err
		}
	}
	return nil
}

// Supported implements platform.Capability.
func (BrowserLauncher) Supported() bool { return true }

// Name returns the logical name of this browser launcher.
func (BrowserLauncher) Name() string { return "linux" }

// --- Terminals ------------------------------------------------------------

// terminalCandidates is the order in which terminal emulators are preferred.
var terminalCandidates = []string{
	"x-terminal-emulator", "gnome-terminal", "kgx", "konsole", "xfce4-terminal",
	"mate-terminal", "lxterminal", "alacritty", "kitty", "foot", "xterm",
}

// detectTerminal picks an available terminal emulator, honoring $TERMINAL.
func detectTerminal() string {
	if term := strings.TrimSpace(os.Getenv("TERMINAL")); term != "" {
		if _, err := exec.LookPath(term); err == nil {
			return term
		}
	}
	for _, c := range terminalCandidates {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}

// TerminalLauncher implements platform.TerminalLauncher.
type TerminalLauncher struct {
	adapter *Adapter
}

// Open opens a terminal in workingDirectory, optionally running command.
func (l TerminalLauncher) Open(workingDirectory, command string) error {
	if l.adapter == nil || l.adapter.terminalEmulator == "" {
		return platform.ErrNotSupported
	}
	emu := l.adapter.terminalEmulator
	args, err := terminalArgs(emu, workingDirectory, command)
	if err != nil {
		return err
	}
	return launch(emu, args...)
}

// Supported implements platform.Capability.
func (l TerminalLauncher) Supported() bool {
	return l.adapter != nil && l.adapter.terminalEmulator != ""
}

// Name returns the logical name of this terminal launcher.
func (TerminalLauncher) Name() string { return "linux" }

// terminalArgs builds the emulator-specific argument list. Commands are
// wrapped in a shell that keeps the terminal open after they finish.
func terminalArgs(emulator, dir, command string) ([]string, error) {
	cmdline := shellCommand(dir, command)

	switch filepath.Base(emulator) {
	case "gnome-terminal", "kgx":
		args := []string{}
		if dir != "" {
			args = append(args, "--working-directory="+dir)
		}
		if cmdline != "" {
			args = append(args, "--", "sh", "-c", cmdline)
		}
		return args, nil
	case "konsole":
		args := []string{}
		if dir != "" {
			args = append(args, "--workdir", dir)
		}
		if cmdline != "" {
			args = append(args, "-e", "sh", "-c", cmdline)
		}
		return args, nil
	case "xfce4-terminal", "mate-terminal", "lxterminal":
		args := []string{}
		if dir != "" {
			args = append(args, "--working-directory="+dir)
		}
		if cmdline != "" {
			args = append(args, "-e", "sh -c "+shellEscape(cmdline))
		}
		return args, nil
	default:
		// xterm, alacritty, kitty, foot, x-terminal-emulator and friends.
		if cmdline == "" {
			return nil, nil
		}
		return []string{"-e", "sh", "-c", cmdline}, nil
	}
}

// shellCommand turns a working directory and optional command into a single
// shell line that leaves an interactive shell open afterwards.
func shellCommand(dir, command string) string {
	var b strings.Builder
	if dir != "" {
		b.WriteString("cd ")
		b.WriteString(shellEscape(dir))
		b.WriteString(" && ")
	}
	if command != "" {
		b.WriteString(command)
		b.WriteString("; ")
	}
	b.WriteString("exec ${SHELL:-sh}")
	return b.String()
}

// shellEscape wraps s in single quotes, doubling any embedded single quotes
// (POSIX shell safe).
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- Services -------------------------------------------------------------

// ServiceManager implements platform.ServiceManager via systemd.
type ServiceManager struct{}

// unitNames returns the unit names to try for a service, most specific first.
func unitNames(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if strings.Contains(name, ".") {
		return []string{name}
	}
	return []string{name, name + ".service"}
}

// Supported implements platform.Capability.
func (ServiceManager) Supported() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

// IsInstalled reports whether a systemd unit for the service exists.
func (ServiceManager) IsInstalled(name string) bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	for _, unit := range unitNames(name) {
		if exec.Command("systemctl", "cat", unit).Run() == nil {
			return true
		}
	}
	return false
}

// IsRunning reports whether a systemd service is active.
func (ServiceManager) IsRunning(name string) bool {
	for _, unit := range unitNames(name) {
		if exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil {
			return true
		}
	}
	return false
}

// Start attempts to start a systemd service.
func (ServiceManager) Start(name string) error {
	for _, unit := range unitNames(name) {
		if err := exec.Command("systemctl", "start", unit).Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("start service %q: %w", name, platform.ErrNotInstalled)
}

// Name returns the logical name of this service manager.
func (ServiceManager) Name() string { return "systemd" }

// --- Processes ------------------------------------------------------------

// ProcessLister implements platform.ProcessLister by reading /proc.
type ProcessLister struct{}

// Supported implements platform.ProcessLister.
func (ProcessLister) Supported() bool { return true }

// List implements platform.ProcessLister.
func (ProcessLister) List() ([]platform.Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	procs := make([]platform.Process, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p, ok := readLinuxProcess(pid)
		if !ok {
			continue
		}
		procs = append(procs, p)
	}
	return procs, nil
}

// readLinuxProcess reads one process's metadata from /proc.
func readLinuxProcess(pid int) (platform.Process, bool) {
	base := "/proc/" + strconv.Itoa(pid)
	name, err := os.ReadFile(base + "/comm")
	if err != nil {
		return platform.Process{}, false
	}
	p := platform.Process{PID: pid, Name: strings.TrimSpace(string(name))}
	if cmdline, err := os.ReadFile(base + "/cmdline"); err == nil {
		for _, arg := range strings.Split(string(cmdline), "\x00") {
			if arg != "" {
				p.Args = append(p.Args, arg)
			}
		}
	}
	if cwd, err := os.Readlink(base + "/cwd"); err == nil {
		p.Cwd = cwd
	}
	return p, true
}
