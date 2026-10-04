package restore

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestInstallerSteps(t *testing.T) {
	installed := map[string]bool{"git": true, "apt-get": true, "sudo": true}
	i := &Installer{
		GOOS:  "linux",
		Tools: []string{"git", "docker", "node"},
		LookPath: func(name string) (string, bool) {
			return "/usr/bin/" + name, installed[name]
		},
		Sudo: false,
	}

	steps := i.Steps()
	if len(steps) != 2 {
		t.Fatalf("Steps = %+v, want 2", steps)
	}
	if steps[0].Name != "docker" || !reflect.DeepEqual(steps[0].Command, []string{"apt-get", "install", "-y", "docker.io"}) {
		t.Errorf("step[0] = %+v", steps[0])
	}
	if steps[1].Name != "node" || !reflect.DeepEqual(steps[1].Command, []string{"apt-get", "install", "-y", "nodejs"}) {
		t.Errorf("step[1] = %+v", steps[1])
	}
}

func TestInstallerInstallRespectsConfirmation(t *testing.T) {
	installed := map[string]bool{"apt-get": true, "git": true}
	var ran [][]string
	i := &Installer{
		GOOS:  "linux",
		Tools: []string{"git", "docker", "node"},
		LookPath: func(name string) (string, bool) {
			return "/usr/bin/" + name, installed[name]
		},
		Run: func(args []string, _ io.Writer) error {
			ran = append(ran, args)
			return nil
		},
	}

	var out bytes.Buffer
	if err := i.Install(&out, NewScanner("y", "n")); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if len(ran) != 1 || ran[0][len(ran[0])-1] != "docker.io" {
		t.Errorf("ran = %v, want only the docker install", ran)
	}
	if !strings.Contains(out.String(), "installed docker") {
		t.Errorf("output = %q", out.String())
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("output should mention the skipped step: %q", out.String())
	}
}

func TestInstallerNoManager(t *testing.T) {
	i := &Installer{
		GOOS:  "linux",
		Tools: []string{"docker"},
		LookPath: func(string) (string, bool) {
			return "", false
		},
	}
	if steps := i.Steps(); steps != nil {
		t.Errorf("Steps = %+v, want nil without a package manager", steps)
	}
	var out bytes.Buffer
	if err := i.Install(&out, NewScanner("y")); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(out.String(), "No supported package manager") {
		t.Errorf("output = %q", out.String())
	}
}
