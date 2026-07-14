package nlq

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFeedbackLogTightensExistingFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feedback.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	(&FeedbackLog{Path: path}).Append(FeedbackRecord{NL: "open work"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("feedback permissions = %o, want 600", got)
	}
}
