package linux

import (
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
