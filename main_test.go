package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewLoggerTightensExistingFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bb.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BB_LOG", path)
	_, closeLog := newLogger()
	closeLog()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log permissions = %o, want 600", got)
	}
}
