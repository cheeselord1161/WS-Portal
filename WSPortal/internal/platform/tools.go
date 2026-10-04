package platform

import (
	"os/exec"
	"sort"
	"strings"
)

// PathToolChecker is a ToolChecker backed by exec.LookPath. It is portable and
// therefore shared by every OS adapter.
type PathToolChecker struct {
	// Tools is the ordered list of tools this checker knows about.
	// The restore engine walks it in order to report dependencies.
	Tools []string
}

// NewPathToolChecker builds a ToolChecker for the named command-line tools.
func NewPathToolChecker(tools ...string) *PathToolChecker {
	sort.Strings(tools)
	return &PathToolChecker{Tools: tools}
}

// LookPath implements ToolChecker.
func (c PathToolChecker) LookPath(name string) (string, bool) {
	if strings.TrimSpace(name) == "" {
		return "", false
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return p, true
}

// Ordered reports the tool names this checker knows about, in the order they
// were listed. It is used by the restore engine to report dependencies in a
// stable order even when the caller did not sort them.
func (c PathToolChecker) Ordered() []string {
	out := make([]string, len(c.Tools))
	copy(out, c.Tools)
	return out
}
