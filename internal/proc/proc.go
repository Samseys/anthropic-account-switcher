// Package proc provides best-effort detection of a running Claude Code process,
// used only to warn before a switch (never to block). A live session can
// rewrite .credentials.json on token refresh and clobber the swap; callers
// only print a warning because detection is heuristic. Shells out to OS tools
// rather than linking platform APIs; platform code is in proc_{unix,windows}.go.
package proc

import "strings"

// ClaudeRunning reports whether a Claude Code process appears to be running.
func ClaudeRunning() bool {
	return running()
}

// mentionsClaude reports whether s names Claude Code but not this tool itself.
func mentionsClaude(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "claude") && !strings.Contains(s, "acc-claude")
}
