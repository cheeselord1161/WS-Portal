package linux

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTerminalArgs(t *testing.T) {
	cases := []struct {
		name     string
		emulator string
		dir      string
		command  string
		want     []string
	}{
		{
			name:     "gnome-terminal with command",
			emulator: "gnome-terminal",
			dir:      "/p",
			command:  "npm run dev",
			want:     []string{"--working-directory=/p", "--", "sh", "-c", "cd '/p' && npm run dev; exec ${SHELL:-sh}"},
		},
		{
			name:     "konsole directory only",
			emulator: "konsole",
			dir:      "/p",
			want:     []string{"--workdir", "/p", "-e", "sh", "-c", "cd '/p' && exec ${SHELL:-sh}"},
		},
		{
			name:     "xfce4-terminal single argument",
			emulator: "xfce4-terminal",
			dir:      "/p",
			command:  "ls",
			want:     []string{"--working-directory=/p", "-e", "sh -c " + shellEscape("cd '/p' && ls; exec ${SHELL:-sh}")},
		},
		{
			name:     "xterm command",
			emulator: "xterm",
			command:  "ls",
			want:     []string{"-e", "sh", "-c", "ls; exec ${SHELL:-sh}"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := terminalArgs(tc.emulator, tc.dir, tc.command)
			if err != nil {
				t.Fatalf("terminalArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("terminalArgs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// writeExec writes an executable stub script to dir/name.
func writeExec(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAppLaunchPrefersNativeExecutable(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "code", "exit 0")
	t.Setenv("PATH", dir)

	bin, prefix, ok := (&Adapter{}).appLaunch("vscode")
	if !ok || bin != "code" || len(prefix) != 0 {
		t.Fatalf("appLaunch = (%q, %v, %v), want (code, [], true)", bin, prefix, ok)
	}
}

func TestAppLaunchFallsBackToFlatpak(t *testing.T) {
	dir := t.TempDir()
	// A flatpak whose `info` succeeds reports the app as installed.
	writeExec(t, dir, "flatpak", `if [ "$1" = "info" ]; then exit 0; fi; exit 1`)
	t.Setenv("PATH", dir)

	bin, prefix, ok := (&Adapter{}).appLaunch("vscode")
	want := []string{"run", "com.visualstudio.code"}
	if !ok || bin != "flatpak" || !reflect.DeepEqual(prefix, want) {
		t.Fatalf("appLaunch = (%q, %v, %v), want (flatpak, %v, true)", bin, prefix, ok, want)
	}
}

func TestAppLaunchFlatpakNotInstalled(t *testing.T) {
	dir := t.TempDir()
	// `flatpak info` fails, so the application is not installed.
	writeExec(t, dir, "flatpak", "exit 1")
	t.Setenv("PATH", dir)

	if bin, _, ok := (&Adapter{}).appLaunch("vscode"); ok {
		t.Fatalf("appLaunch = %q, want no resolution when flatpak info fails", bin)
	}
}

func TestAppLaunchFallsBackToSnap(t *testing.T) {
	dir := t.TempDir()
	// A snap whose `list` succeeds reports the app as installed.
	writeExec(t, dir, "snap", `if [ "$1" = "list" ]; then exit 0; fi; exit 1`)
	t.Setenv("PATH", dir)

	bin, prefix, ok := (&Adapter{}).appLaunch("vscode")
	want := []string{"run", "code"}
	if !ok || bin != "snap" || !reflect.DeepEqual(prefix, want) {
		t.Fatalf("appLaunch = (%q, %v, %v), want (snap, %v, true)", bin, prefix, ok, want)
	}
}

func TestShellEscape(t *testing.T) {
	got := shellEscape("don't")
	want := `'don'\''t'`
	if got != want {
		t.Errorf("shellEscape = %q, want %q", got, want)
	}
}

func TestUnitNames(t *testing.T) {
	cases := map[string][]string{
		"docker":      {"docker", "docker.service"},
		"foo.service": {"foo.service"},
		"":            nil,
	}
	for in, want := range cases {
		got := unitNames(in)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("unitNames(%q) = %v, want %v", in, got, want)
		}
	}
}
