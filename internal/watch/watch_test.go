package watch

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

// A write inside the watched dir fires notify (after the debounce window).
func TestWatchFiresOnFileChange(t *testing.T) {
	dir := t.TempDir()
	var n atomic.Int32
	stop := Start(dir, 50*time.Millisecond, time.Hour, func() { n.Add(1) }, log.New(io.Discard))
	defer stop()
	time.Sleep(30 * time.Millisecond) // let the watch goroutine subscribe
	if err := os.WriteFile(filepath.Join(dir, "last-touched"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return n.Load() >= 1 }, 2*time.Second) {
		t.Fatal("notify never fired on a file change")
	}
}

// With no file activity, the slow poll still nudges notify — the fallback.
func TestWatchPollFallback(t *testing.T) {
	dir := t.TempDir()
	var n atomic.Int32
	stop := Start(dir, time.Hour, 60*time.Millisecond, func() { n.Add(1) }, log.New(io.Discard))
	defer stop()
	if !waitFor(func() bool { return n.Load() >= 1 }, 2*time.Second) {
		t.Fatal("poll fallback never fired")
	}
}

// A missing store dir degrades to poll-only rather than failing.
func TestWatchMissingDirDegradesToPoll(t *testing.T) {
	var n atomic.Int32
	stop := Start(filepath.Join(t.TempDir(), "nope"), time.Hour, 60*time.Millisecond,
		func() { n.Add(1) }, log.New(io.Discard))
	defer stop()
	if !waitFor(func() bool { return n.Load() >= 1 }, 2*time.Second) {
		t.Fatal("poll-only fallback never fired for a missing dir")
	}
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}
