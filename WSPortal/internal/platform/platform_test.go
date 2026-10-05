package platform_test

import (
	"errors"
	"testing"

	"github.com/cheeselord1161/WS_Portal/internal/platform"
	"github.com/cheeselord1161/WS_Portal/internal/platform/detect"
)

func TestDetectForKnownOS(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		a := detect.For(goos)
		if a == nil {
			t.Fatalf("detect.For(%q) returned nil", goos)
		}
		if a.OS != goos {
			t.Errorf("detect.For(%q).OS = %q", goos, a.OS)
		}
		if a.Tools == nil {
			t.Errorf("detect.For(%q) has no tool checker", goos)
		}
		if !detect.Supported(goos) {
			t.Errorf("detect.Supported(%q) = false", goos)
		}
	}
}

func TestDetectDarwinIsUnsupported(t *testing.T) {
	// macOS support was removed; it must fall back to the unsupported adapter
	// rather than a dedicated one.
	if detect.Supported("darwin") {
		t.Error("detect.Supported(\"darwin\") should be false")
	}
	a := detect.For("darwin")
	if a.OS != "darwin" {
		t.Errorf("detect.For(\"darwin\").OS = %q", a.OS)
	}
	if err := a.Apps.Launch("vscode"); !errors.Is(err, platform.ErrNotSupported) {
		t.Errorf("Launch error = %v, want ErrNotSupported", err)
	}
}

func TestDetectUnknownOSIsUnsupportedButUsable(t *testing.T) {
	a := detect.For("plan9")
	if a.OS != "plan9" {
		t.Errorf("OS = %q", a.OS)
	}
	if detect.Supported("plan9") {
		t.Error("plan9 should not be reported as supported")
	}
	// Read-only capability still works on unknown platforms.
	if _, ok := a.Tools.LookPath("definitely-not-a-real-binary-xyz"); ok {
		t.Error("LookPath should not find a nonexistent binary")
	}
	if err := a.Apps.Launch("vscode"); !errors.Is(err, platform.ErrNotSupported) {
		t.Errorf("Launch error = %v, want ErrNotSupported", err)
	}
}

func TestUnsupportedStubs(t *testing.T) {
	if err := (platform.UnsupportedBrowsers{}).Open("chrome", nil); !errors.Is(err, platform.ErrNotSupported) {
		t.Errorf("browser error = %v", err)
	}
	if err := (platform.UnsupportedTerminals{}).Open("", ""); !errors.Is(err, platform.ErrNotSupported) {
		t.Errorf("terminal error = %v", err)
	}
	if err := (platform.UnsupportedServices{}).Start("docker"); !errors.Is(err, platform.ErrNotSupported) {
		t.Errorf("service error = %v", err)
	}
}

func TestPathToolCheckerFindsShellBuiltins(t *testing.T) {
	tc := platform.PathToolChecker{}
	if _, ok := tc.LookPath(""); ok {
		t.Error("empty tool name should not be found")
	}
	// `go` is guaranteed present in the test environment.
	if _, ok := tc.LookPath("go"); !ok {
		t.Skip("go not on PATH in this environment")
	}
}
