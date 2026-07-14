// Package watch signals when the beads store changes on disk — an agent (or
// any bd command) mutating the tracker from another terminal. The TUI reads
// live data through `bd list`; this only tells it WHEN to reload.
//
// Empirically (bd 1.1.0), every mutation writes `.beads/last-touched`
// instantly, while `.beads/issues.jsonl` is a debounced ~30s export written
// temp-then-rename. Watching the `.beads` directory catches both. A short
// debounce collapses the export's write flurry into one reload; a slow poll
// backstops anything the file events miss.
package watch

import (
	"time"

	"github.com/charmbracelet/log"
	"github.com/fsnotify/fsnotify"
)

// Start watches beadsDir for external mutations, calling notify (debounced by
// `debounce`) on each change, and unconditionally every `pollEvery` as a
// fallback. It returns a stop func. Setup failures degrade to poll-only
// rather than erroring — a missing signal is worse than a redundant reload
// (the caller de-dupes unchanged reloads).
func Start(beadsDir string, debounce, pollEvery time.Duration, notify func(), logger *log.Logger) func() {
	done := make(chan struct{})

	w, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("watch: fsnotify unavailable, polling only", "err", err)
		go poll(pollEvery, notify, done)
		return func() { close(done) }
	}
	if err := w.Add(beadsDir); err != nil {
		logger.Warn("watch: cannot watch store dir, polling only", "dir", beadsDir, "err", err)
		_ = w.Close()
		go poll(pollEvery, notify, done)
		return func() { close(done) }
	}
	logger.Info("watch: watching store", "dir", beadsDir, "debounce", debounce, "poll", pollEvery)

	go func() {
		defer w.Close()
		var timer *time.Timer
		var fire <-chan time.Time
		poll := time.NewTicker(pollEvery)
		defer poll.Stop()
		for {
			select {
			case <-done:
				return
			case _, ok := <-w.Events:
				if !ok {
					return
				}
				// Reset the debounce window; the export writes a burst.
				if timer == nil {
					timer = time.NewTimer(debounce)
				} else {
					timer.Reset(debounce)
				}
				fire = timer.C
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				logger.Warn("watch: fsnotify error", "err", err)
			case <-fire:
				fire = nil
				notify()
			case <-poll.C:
				notify()
			}
		}
	}()
	return func() { close(done) }
}

// poll is the degraded path: no file events, just a periodic nudge.
func poll(every time.Duration, notify func(), done <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			notify()
		}
	}
}
