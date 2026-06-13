//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// running uses ps (not pgrep) for full argv, so node-hosted claude-code is also matched.
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
