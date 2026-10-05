// Package detect chooses the platform adapter for the current operating
// system.
//
// It lives in its own package to avoid an import cycle: the platform package
// defines the interfaces, and the per-OS packages implement them, so neither
// can own the selection logic.
package detect

import (
	"runtime"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/platform/linux"
	"github.com/cheeselord1161/WS_Portal/internal/platform/windows"
)

// Current returns the adapter for the running operating system.
//
// Unknown operating systems get a fully unsupported adapter rather than an
// error, so that read-only commands such as inspect and validate still work
// everywhere.
func Current() *platform.Adapter {
	return For(runtime.GOOS)
}

// For returns the adapter for the named GOOS value.
func For(goos string) *platform.Adapter {
	switch goos {
	case linux.OS:
		return linux.New().Bundle()
	case windows.OS:
		return windows.New().Bundle()
	default:
		return &platform.Adapter{
			OS:        goos,
			Apps:      platform.UnsupportedApps{},
			Browsers:  platform.UnsupportedBrowsers{},
			Terminals: platform.UnsupportedTerminals{},
			Services:  platform.UnsupportedServices{},
			Tools:     platform.PathToolChecker{},
			Processes: platform.UnsupportedProcesses{},
		}
	}
}

// Supported reports whether goos has a dedicated adapter.
func Supported(goos string) bool {
	switch goos {
	case linux.OS, windows.OS:
		return true
	default:
		return false
	}
}
