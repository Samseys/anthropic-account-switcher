// Package lock provides a cross-process advisory lock on ~/.claude/account-profiles/.lock.
// The kernel releases it when the holder exits, so there is no stale-lock state.
// On NFS/SMB the guarantee is only as strong as the filesystem's remote lock support.
package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

const (
	lockName   = ".lock"
	retryEvery = 50 * time.Millisecond // poll interval while waiting
)

// retryFor is a var so tests can shrink it.
var retryFor = 2 * time.Second

func lockPath() string {
	return filepath.Join(paths.ProfileDir, lockName)
}

// Acquire takes the profile-directory lock and returns a release function.
// Retries for retryFor on contention, then fails.
func Acquire() (func(), error) {
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(retryFor)
	for {
		err := flockTry(f)
		if err == nil {
			released := false
			return func() {
				if released {
					return
				}
				released = true
				_ = flockRelease(f)
				_ = f.Close()
			}, nil
		}
		if err != errContended {
			_ = f.Close()
			return nil, fmt.Errorf("locking %s: %w", lockPath(), err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another %s is running; retry in a moment", paths.Bin)
		}
		time.Sleep(retryEvery)
	}
}

// errContended is the only error Acquire retries on.
var errContended = fmt.Errorf("lock is held")
