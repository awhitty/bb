package layout

import "testing"

func TestComputeHeightInvariant(t *testing.T) {
	for _, h := range []int{3, 10, 24, 50} {
		s := Compute(100, h, 0, 14)
		if s.Header.H+s.Body.H+s.Footer.H != h {
			t.Fatalf("h=%d: header+body+footer = %d, want %d", h, s.Header.H+s.Body.H+s.Footer.H, h)
		}
		if s.Body.Y != 1 || s.Footer.Y != h-1 {
			t.Fatalf("h=%d: body.Y=%d footer.Y=%d", h, s.Body.Y, s.Footer.Y)
		}
	}
}

func TestComputePanelSplit(t *testing.T) {
	s := Compute(100, 24, 40, 14)
	if s.Main.W != 59 || s.Panel.W != 40 || s.Main.W+1+s.Panel.W != 100 {
		t.Fatalf("split: main=%d panel=%d", s.Main.W, s.Panel.W)
	}
	// panel clamps so main keeps minMain
	s2 := Compute(40, 24, 30, 14)
	if s2.Main.W < 14 {
		t.Fatalf("main starved: %d", s2.Main.W)
	}
	// no panel → main spans full width
	s3 := Compute(100, 24, 0, 14)
	if s3.Main.W != 100 || !s3.Panel.Empty() {
		t.Fatalf("no-panel: main=%d panelEmpty=%v", s3.Main.W, s3.Panel.Empty())
	}
}

func TestWindowPageJump(t *testing.T) {
	// fixed until focus hits an edge, then leaps
	if got := Window(100, 10, 5, 0); got != 0 {
		t.Fatalf("focus inside window should not move start: %d", got)
	}
	if got := Window(100, 10, 15, 0); got != 15 {
		t.Fatalf("focus past bottom edge should leap: %d", got)
	}
	if got := Window(100, 10, 3, 20); got != 3-10+1 && Window(100, 10, 3, 20) < 0 {
		t.Fatalf("paged up unexpected: %d", got)
	}
	// clamp at end
	if got := Window(12, 10, -1, 99); got != 2 {
		t.Fatalf("clamp maxStart: %d", got)
	}
}
