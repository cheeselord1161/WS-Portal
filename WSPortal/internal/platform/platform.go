// Package platform defines the OS-facing capabilities WSPortal needs.
//
// The core of WSPortal never shells out to a platform-specific command
// directly. Instead it depends on the small interfaces declared here, and a
// per-OS adapter (see the windows and linux subpackages) implements them. This
// keeps the core portable and makes the platform layer mockable in tests.
package platform

// Application is a logical application resolved against the current machine.
//
// The core never executes an arbitrary string from a workspace: a logical id is
// resolved against a trusted, platform-specific mapping, and only a resolved
// application can be launched.
type Application struct {
	// ID is the canonical, portable identifier, e.g. "vscode".
	ID string
	// Name is the human-friendly name, e.g. "VS Code", used in the restore plan.
	Name string
	// Executable is the local executable that provides the application, when
	// it is installed. It is informational; callers must launch through the
	// AppLauncher rather than executing it directly.
	Executable string
	// Known is true when the id maps to a catalogued application.
	Known bool
	// Installed is true when the application is installed on this machine.
	Installed bool
}

// AppLauncher resolves and launches desktop applications by logical id or name.
type AppLauncher interface {
	// Resolve maps a logical application id, alias, or display name to its
	// local representation. An unrecognised reference resolves to an
	// Application with Known=false rather than an error, so callers can report
	// it clearly. It returns ErrNotSupported when the adapter cannot resolve
	// applications at all.
	Resolve(app string) (Application, error)

	// Launch opens the named application, optionally passing args such as a
	// file or folder to open. The reference is resolved first; an unknown or
	// uninstalled application is never launched. It returns ErrNotSupported
	// when the adapter cannot launch applications.
	Launch(app string, args ...string) error

	// Name is the logical name this launcher knows, e.g. "vscode". Used by the
	// restore engine to match a workspace application to this launcher.
	Name() string
}

// BrowserLauncher opens browser sessions and tabs.
type BrowserLauncher interface {
	// Open opens urls in the named browser. An empty browser name means the
	// user's default browser.
	Open(browser string, urls []string) error

	// Name is the logical name, e.g. "chrome".
	Name() string
}

// TerminalLauncher opens terminal sessions, optionally running a command.
type TerminalLauncher interface {
	// Open opens a terminal in workingDirectory. When command is non-empty the
	// terminal should run it. Implementations must not run commands without
	// the caller having obtained user confirmation.
	Open(workingDirectory string, command string) error

	// Name is the logical name, e.g. "terminal".
	Name() string
}

// ServiceManager inspects and starts long-running services such as databases
// and container engines.
type ServiceManager interface {
	// IsInstalled reports whether a service is available on this machine.
	IsInstalled(name string) bool

	// IsRunning reports whether a service is currently running.
	IsRunning(name string) bool

	// Start attempts to start a service.
	Start(name string) error

	// Name is the logical name, e.g. "docker".
	Name() string
}

// Process is a snapshot of a running process, as far as the platform can see
// it. Cwd may be empty when the platform cannot report it.
type Process struct {
	// PID is the process id.
	PID int
	// Name is the executable name, e.g. "code" or "bash".
	Name string
	// Args is the argument vector, with Args[0] normally the executable.
	Args []string
	// Cwd is the process working directory, when known.
	Cwd string
}

// ProcessLister lists the processes running on this machine. The capture
// engine uses it to recognize applications, terminals, and projects.
type ProcessLister interface {
	// Supported reports whether process listing works on this machine.
	Supported() bool
	// List returns a snapshot of the running processes.
	List() ([]Process, error)
}

// ToolChecker reports whether command-line tools are available.
type ToolChecker interface {
	// LookPath reports the resolved path of a tool and whether it was found.
	LookPath(name string) (string, bool)

	// Ordered reports the tool names this checker knows about, in the order
	// they were listed. The restore engine uses this to report dependencies
	// in a stable order.
	Ordered() []string
}

// Capability is an optional interface an adapter can implement to report
// whether a capability is actually usable on this machine. The restore planner
// uses it to mark steps that cannot be executed. A non-nil capability that does
// not implement Capability is assumed to be supported.
type Capability interface {
	// Supported reports whether this capability can run on the current machine.
	Supported() bool
}

// Adapter bundles every capability for one operating system. A nil field means
// the capability is not yet implemented on that platform.
type Adapter struct {
	// OS is the runtime.GOOS value this adapter targets.
	OS string

	// Apps launches desktop applications.
	Apps AppLauncher

	// Browsers opens browser sessions.
	Browsers BrowserLauncher

	// Terminals opens terminal sessions.
	Terminals TerminalLauncher

	// Services inspects and starts services.
	Services ServiceManager

	// Tools checks for command-line tools.
	Tools ToolChecker

	// Processes lists running processes, for automatic capture.
	Processes ProcessLister
}
