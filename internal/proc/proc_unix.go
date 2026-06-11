//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// running lists every process with its full command line and looks for one
// mentioning "claude" that isn't this tool. ps (not pgrep) is used because its
// output reliably carries the full argv on both Linux and macOS, so a
// node-hosted .../claude-code/cli.js is matched, not just a native claude
// binary.
func running() bool {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,args=").Output()
	if err != nil {
		return false
	}
	self := strconv.Itoa(os.Getpid())
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, args, _ := strings.Cut(line, " ")
		if pid == self {
			continue
		}
		if mentionsClaude(args) {
			return true
		}
	}
	return false
}
