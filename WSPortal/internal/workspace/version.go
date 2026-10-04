package workspace

import "fmt"

// IsSupportedVersion reports whether this build can read the given schema
// version.
func IsSupportedVersion(version int) bool {
	return version >= 1 && version <= CurrentVersion
}

// CheckVersion returns an error when a workspace declares a schema version this
// build cannot handle. Future versions are rejected; older versions are
// accepted and migrated on load. Version 1 workspaces remain readable because
// their flat browser tabs are migrated into a single window.
func (w *Workspace) CheckVersion() error {
	if w == nil {
		return fmt.Errorf("workspace is nil")
	}
	if w.Version <= 0 {
		return fmt.Errorf("workspace is missing a schema version")
	}
	if w.Version > CurrentVersion {
		return fmt.Errorf(
			"workspace schema version %d is newer than this build supports (max %d); upgrade ws",
			w.Version, CurrentVersion,
		)
	}
	return nil
}

// migrate upgrades an older in-memory workspace to the current schema version.
//
// Migrations are applied in order. The v1 -> v2 change (flat browser tabs to
// window-grouped tabs) is performed by normalize; this only advances the
// recorded version.
func (w *Workspace) migrate() error {
	if w.Version < 2 {
		w.Version = 2
	}
	return nil
}
