package lock

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

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
	// Shrink so the give-up path is exercised quickly without sleeping 2s.
	old := retryFor
	retryFor = 100 * time.Millisecond
	t.Cleanup(func() { retryFor = old })

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

func TestLeftoverLockFileDoesNotBlock(t *testing.T) {
	setup(t)
	if err := os.MkdirAll(paths.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A leftover lock file (including old PID-file format) must not block.
	if err := os.WriteFile(lockPath(), []byte(`{"pid":12345,"time":"2020-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rel, err := Acquire()
	if err != nil {
		t.Fatalf("a leftover lock file blocked the acquire: %v", err)
	}
	defer rel()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("acquiring over a leftover lock file took %v; expected near-instant", d)
	}
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
