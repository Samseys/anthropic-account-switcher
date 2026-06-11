// Package lock provides a cross-process advisory lock guarding the profile
// directory, so two concurrent claude-acc invocations can't interleave a
// credential write with a config patch, or race two saves against the same
// profile directory.
//
// It is pure Go and cross-platform: the lock is a single file created with
// O_CREATE|O_EXCL holding the owner's PID and timestamp. A lock left behind by
// a crashed process (dead PID, or simply older than ~30s) is stolen, so the
// tool never wedges itself.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

const (
	lockName   = ".lock"
	staleAfter = 30 * time.Second      // a lock older than this is stolen
	retryFor   = 2 * time.Second       // total time to wait on contention
	retryEvery = 50 * time.Millisecond // poll interval while waiting
)

// info is the JSON payload written into the lockfile, used to decide whether a
// contended lock is stale.
type info struct {
	PID  int       `json:"pid"`
	Time time.Time `json:"time"`
	Host string    `json:"host,omitempty"`
}

func lockPath() string {
	return filepath.Join(paths.ProfileDir, lockName)
}

// Acquire takes the profile-directory lock and returns a release function the
// caller must defer. It retries briefly on contention and steals a stale lock
// (dead PID on this host, or one older than ~30s). On sustained contention it
// fails with a "another claude-acc is running" error.
func Acquire() (func(), error) {
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(retryFor)
	for {
		release, err := tryAcquire()
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// The lock is held. Steal it if it's stale, otherwise wait and retry.
		if stealStale() {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another %s is running; retry in a moment", paths.Bin)
		}
		time.Sleep(retryEvery)
	}
}

// tryAcquire attempts a single exclusive create of the lockfile. It returns
// os.ErrExist (wrapped) when the lock is already held.
func tryAcquire() (func(), error) {
	f, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	data, _ := json.Marshal(info{PID: os.Getpid(), Time: time.Now(), Host: host})
	_, _ = f.Write(data)
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(lockPath())
		return nil, cerr
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = os.Remove(lockPath())
	}, nil
}

// stealStale removes the current lockfile if it looks abandoned, reporting
// whether the caller should retry the acquire immediately. A lockfile that
// vanished on its own, is unparseable, is older than staleAfter, or whose PID
// is dead on this host is considered stale.
func stealStale() bool {
	b, err := os.ReadFile(lockPath())
	if err != nil {
		// It disappeared between the failed create and this read; retry.
		return errors.Is(err, os.ErrNotExist)
	}
	var held info
	if json.Unmarshal(b, &held) != nil {
		_ = os.Remove(lockPath())
		return true
	}
	stale := time.Since(held.Time) > staleAfter
	if !stale {
		// Only trust PID liveness when the lock was taken on this machine;
		// PIDs are meaningless across hosts (e.g. a shared profile dir).
		if host, _ := os.Hostname(); host == held.Host && !processAlive(held.PID) {
			stale = true
		}
	}
	if stale {
		_ = os.Remove(lockPath())
		return true
	}
	return false
}
