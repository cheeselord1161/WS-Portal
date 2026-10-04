package workspace

import "testing"

func TestRegexCheck(t *testing.T) {
	tests := []string{
		"${WORKSPACE_ROOT}/myproject",
		"${PATH:-/usr/bin}:${NOPE:-/default}",
		"${VAR:-default}",
		"hello world",
	}
	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			matches := varPattern.FindAllStringSubmatchIndex(s, -1)
			t.Logf("input=%q matches=%v", s, matches)
			for _, m := range matches {
				t.Logf("  full=[%d,%d) name=[%d,%d) def=[%d,%d)", m[0], m[1], m[2], m[3], m[4], m[5])
			}
		})
	}
}
