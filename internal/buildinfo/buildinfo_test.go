package buildinfo

import "testing"

func TestOverride(t *testing.T) {
	old := override
	override = "v1.2.3"
	t.Cleanup(func() { override = old })
	if got := Version(); got != "v1.2.3" {
		t.Fatalf("Version() = %q, want v1.2.3", got)
	}
}
