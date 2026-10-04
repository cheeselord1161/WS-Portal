package platform

// UnsupportedApps is an AppLauncher that reports every application as
// unavailable and refuses to launch. OS adapters embed it until real
// implementations land.
type UnsupportedApps struct{}

// Launch implements AppLauncher.
func (UnsupportedApps) Launch(string, ...string) error { return ErrNotSupported }

// Available implements AppLauncher.
func (UnsupportedApps) Available(string) bool { return false }

// Name implements AppLauncher.
func (UnsupportedApps) Name() string { return "" }

// Supported implements Capability. Application launching is unavailable.
func (UnsupportedApps) Supported() bool { return false }

// UnsupportedBrowsers is a BrowserLauncher that always fails.
type UnsupportedBrowsers struct{}

// Open implements BrowserLauncher.
func (UnsupportedBrowsers) Open(string, []string) error { return ErrNotSupported }

// Name implements BrowserLauncher.
func (UnsupportedBrowsers) Name() string { return "" }

// Supported implements Capability. Browser launching is unavailable.
func (UnsupportedBrowsers) Supported() bool { return false }

// UnsupportedTerminals is a TerminalLauncher that always fails.
type UnsupportedTerminals struct{}

// Open implements TerminalLauncher.
func (UnsupportedTerminals) Open(string, string) error { return ErrNotSupported }

// Name implements TerminalLauncher.
func (UnsupportedTerminals) Name() string { return "" }

// Supported implements Capability. Terminal launching is unavailable.
func (UnsupportedTerminals) Supported() bool { return false }

// UnsupportedServices is a ServiceManager that knows nothing about services.
type UnsupportedServices struct{}

// IsInstalled implements ServiceManager.
func (UnsupportedServices) IsInstalled(string) bool { return false }

// IsRunning implements ServiceManager.
func (UnsupportedServices) IsRunning(string) bool { return false }

// Start implements ServiceManager.
func (UnsupportedServices) Start(string) error { return ErrNotSupported }

// Name implements ServiceManager.
func (UnsupportedServices) Name() string { return "" }

// Supported implements Capability. Service management is unavailable.
func (UnsupportedServices) Supported() bool { return false }

// UnsupportedProcesses is a ProcessLister that cannot see any processes.
type UnsupportedProcesses struct{}

// Supported implements ProcessLister.
func (UnsupportedProcesses) Supported() bool { return false }

// List implements ProcessLister.
func (UnsupportedProcesses) List() ([]Process, error) { return nil, ErrNotSupported }
