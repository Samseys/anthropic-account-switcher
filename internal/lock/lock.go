// Package lock provides a cross-process advisory lock guarding the profile
// directory, so two concurrent acc-claude invocations can't interleave a
// credential write with a config patch, or race two saves against the same
// profile directory.
//
// It is pure Go and cross-platform: the lock is a single file holding the
// owner's PID and timestamp, created atomically by writing a temp file and
// hard-linking it into place (link fails if the target exists, and the lock is
// never observable half-written). A lock left behind by a crashed process (dead
// PID, or simply older than ~30s) is stolen, so the tool never wedges itself.
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
	retryEvery = 50 * time.Millisecond // poll interval while waiting
)

// retryFor is the total time Acquire waits on contention before giving up. It's
// a var, not a const, so tests can shrink it to exercise the give-up path
// without sleeping out the full production ceiling.
var retryFor = 2 * time.Second

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
// fails with a "another acc-claude is running" error.
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
//
// The fully-populated payload is written to a temp file first and then linked
// into place: os.Link is atomic and fails if the target exists, so the lock
// never appears as an empty, half-written file that a racing stealStale could
// mistake for an unparseable (and therefore stale) lock and steal out from
// under its live owner.
func tryAcquire() (func(), error) {
	host, _ := os.Hostname()
	data, _ := json.Marshal(info{PID: os.Getpid(), Time: time.Now(), Host: host})

	tmp, err := os.CreateTemp(paths.ProfileDir, ".lock-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return nil, werr
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpName)
		return nil, cerr
	}
	// Link fails with ErrExist when the lock is already held; that error must
	// propagate so Acquire's retry/steal loop sees it.
	if lerr := os.Link(tmpName, lockPath()); lerr != nil {
		_ = os.Remove(tmpName)
		return nil, lerr
	}
	_ = os.Remove(tmpName)

	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = removeLock()
	}, nil
}

// removeLock deletes the lockfile, retrying briefly. On Windows a delete fails
// with a sharing violation while another goroutine or process has the file open
// for reading (stealStale, below); that open is sub-millisecond, so a short
// retry clears it. Without the retry the owner's release would silently leave
// the lock behind and wedge every other waiter. A missing file is success.
func removeLock() error {
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Remove(lockPath())
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
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
		_ = removeLock()
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
		_ = removeLock()
		return true
	}
	return false
}
