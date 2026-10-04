package workspace

import (
	"strings"
	"time"
)

// nowRFC3339 returns the current UTC time as an RFC3339 string. It is a
// package-level variable so tests can substitute a fixed clock.
var nowRFC3339 = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// New returns a minimal, valid workspace with sensible defaults.
//
// The generated project path uses the portable ${WORKSPACE_ROOT} variable so
// the file works on any machine.
func New(name, description string) *Workspace {
	name = strings.TrimSpace(name)
	return &Workspace{
		Version: CurrentVersion,
		Workspace: Identity{
			Name:        name,
			Description: strings.TrimSpace(description),
		},
		Project: &Project{
			Name: name,
			Path: "${" + VarWorkspaceRoot + "}/" + name,
		},
		Environment: Environment{Runtime: map[string]string{}},
		Metadata: Metadata{
			CreatedBy: "wsportal",
			CreatedAt: nowRFC3339(),
		},
	}
}
