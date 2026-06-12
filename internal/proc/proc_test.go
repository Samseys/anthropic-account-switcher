package proc

import "testing"

func TestMentionsClaude(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// The modern native binary, by image name (Windows) or argv (Unix).
		{"claude.exe", true},
		{"/usr/local/bin/claude", true},
		{"Claude.exe", true}, // case-insensitive
		// A legacy node-hosted session, visible only in the command line.
		{"node /home/u/.nvm/.../claude-code/cli.js", true},
		// This tool itself must never trigger its own warning.
		{"acc-claude.exe", false},
		{"C:\\Users\\u\\AppData\\Local\\acc-claude\\acc-claude.exe switch work", false},
		{"ACC-CLAUDE", false},
		// Unrelated processes.
		{"chrome.exe", false},
		{"", false},
	}
	for _, c := range cases {
		if got := mentionsClaude(c.in); got != c.want {
			t.Errorf("mentionsClaude(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
