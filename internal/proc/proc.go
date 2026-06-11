// Package proc provides best-effort detection of a running Claude Code process.
// It is used to warn (never to block) before a switch: a live Claude Code
// session can rewrite .credentials.json on a token refresh and clobber the
// swap. Detection is heuristic, so a false positive must never stop the user —
// callers only print a warning.
//
// Consistent with the rest of the tool, it shells out to the OS process tools
// rather than linking platform APIs. The platform-specific scan lives in
// proc_windows.go / proc_unix.go.
package proc

import "strings"

// ClaudeRunning reports, best-effort, whether a Claude Code process appears to
// be running. Any error (tool missing, nothing matched) yields false.
func ClaudeRunning() bool {
	return running()
}

// mentionsClaude reports whether s names Claude Code while excluding this tool
// itself (claude-acc), whose own name would otherwise always match.
func mentionsClaude(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "claude") && !strings.Contains(s, "claude-acc")
}
