package platform

import "errors"

// ErrNotSupported is returned by an adapter when a capability is not
// implemented for the current operating system.
var ErrNotSupported = errors.New("not supported on this platform")

// ErrNotInstalled is returned when an application or tool is not installed.
var ErrNotInstalled = errors.New("not installed")

// ErrNotRunning is returned when a service is installed but not running.
var ErrNotRunning = errors.New("not running")
