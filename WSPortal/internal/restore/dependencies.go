package restore

import (
	"fmt"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// NewDependencyChecker returns a checker for the current machine. It consults
// the platform's ToolChecker and the workspace's declared runtimes.
func NewDependencyChecker(p *platform.Adapter, ws *workspace.Workspace) *DependencyChecker {
	c := &DependencyChecker{Platform: p}
	if p != nil {
		c.Tools = p.Tools
	}
	if ws != nil {
		c.Runtimes = ws.Environment.Runtime
	}
	return c
}

// DependencyChecker inspects the machine against a workspace's declared
// dependencies and produces status lines for the restore plan.
type DependencyChecker struct {
	Platform *platform.Adapter
	Tools    platform.ToolChecker
	Runtimes map[string]string
}

// Check returns one status line per declared tool and runtime, in execution
// order. A missing dependency is reported as a warning so the user can fix it
// before attempting a restore.
func (c *DependencyChecker) Check() []string {
	var out []string

	if c.Tools != nil {
		for _, tool := range c.Tools.Ordered() {
			if _, ok := c.Tools.LookPath(tool); ok {
				out = append(out, fmt.Sprintf("  ✓ %s", tool))
			} else {
				out = append(out, fmt.Sprintf("  ✗ %s (not installed)", tool))
			}
		}
	}

	if c.Runtimes != nil {
		for name, version := range c.Runtimes {
			out = append(out, fmt.Sprintf("  runtime: %s %s", name, version))
		}
	}

	return out
}

// CheckTools is a lighter-weight check that returns only the binary tools.
func (c *DependencyChecker) CheckTools() []string {
	var out []string
	if c.Tools == nil {
		return out
	}

	for _, tool := range c.Tools.Ordered() {
		if path, ok := c.Tools.LookPath(tool); ok {
			out = append(out, fmt.Sprintf("  ✓ %s (%s)", tool, path))
		} else {
			out = append(out, fmt.Sprintf("  ✗ %s (not installed)", tool))
		}
	}
	return out
}
