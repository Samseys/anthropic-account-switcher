package lock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// setup points paths.ProfileDir at a scratch directory for the test.
func setup(t *testing.T) {
	t.Helper()
	old := paths.ProfileDir
	paths.ProfileDir = filepath.Join(t.TempDir(), "account-profiles")
	t.Cleanup(func() { paths.ProfileDir = old })
}

func TestAcquireReleaseReacquire(t *testing.T) {
	setup(t)
	rel, err := Acquire()
	if err != nil {
		t.Fatal(err)
	}
	rel()
	rel2, err := Acquire()
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	rel2()
	// Release must be idempotent.
	rel2()
}

func TestContendedAcquireFails(t *testing.T) {
	setup(t)
	rel, err := Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer rel()

	start := time.Now()
	if _, err := Acquire(); err == nil {
		t.Fatal("expected contended acquire to fail while the lock is held")
	} else if !strings.Contains(err.Error(), "another") {
		t.Fatalf("error = %v, want an 'another ... running' message", err)
	}
	if d := time.Since(start); d < retryFor {
		t.Fatalf("gave up after %v; expected to retry for ~%v first", d, retryFor)
	}
}

func TestStealsStaleLockByAge(t *testing.T) {
	setup(t)
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	// Fresh PID (our own, alive) but an old timestamp: the age check alone must
	// make this stale.
	old := info{PID: os.Getpid(), Time: time.Now().Add(-time.Hour), Host: host}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(lockPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rel, err := Acquire()
	if err != nil {
		t.Fatalf("expected to steal the stale lock, got %v", err)
	}
	defer rel()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("stealing a stale lock took %v; expected it to be near-instant", d)
	}
}

func TestStealsLockOfDeadProcess(t *testing.T) {
	setup(t)
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	// Fresh timestamp but a PID that cannot be alive: the liveness check must
	// make this stale.
	dead := info{PID: 1 << 30, Time: time.Now(), Host: host}
	data, _ := json.Marshal(dead)
	if err := os.WriteFile(lockPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := Acquire()
	if err != nil {
		t.Fatalf("expected to steal the dead process's lock, got %v", err)
	}
	rel()
}

func TestConcurrentAcquireSerializes(t *testing.T) {
	setup(t)
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const n = 5
	var (
		wg                sync.WaitGroup
		mu                sync.Mutex
		active, maxActive int
	)
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := Acquire()
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			active++
			maxActive = max(maxActive, active)
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			rel()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent acquire failed: %v", err)
	}
	if maxActive > 1 {
		t.Fatalf("lock allowed %d concurrent holders; must serialize to 1", maxActive)
	}
}
