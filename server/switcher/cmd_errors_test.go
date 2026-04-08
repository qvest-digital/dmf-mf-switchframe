package switcher

import "testing"

func TestRecentCommandErrors_Ring(t *testing.T) {
	r := newRecentErrors(8)
	r.record(1, "source not found")
	r.record(3, "bad input")
	errs := r.snapshot()
	if len(errs) != 2 {
		t.Fatalf("expected 2, got %d", len(errs))
	}
	if errs[0].Seq != 1 || errs[1].Seq != 3 {
		t.Errorf("unexpected: %+v", errs)
	}
}

func TestRecentCommandErrors_Eviction(t *testing.T) {
	r := newRecentErrors(2)
	r.record(1, "a")
	r.record(2, "b")
	r.record(3, "c")
	errs := r.snapshot()
	if len(errs) != 2 || errs[0].Seq != 2 {
		t.Errorf("expected [2,3], got %+v", errs)
	}
}

func TestRecentCommandErrors_IgnoresSuccess(t *testing.T) {
	r := newRecentErrors(8)
	r.record(1, "err")
	r.record(2, "")
	errs := r.snapshot()
	if len(errs) != 1 {
		t.Fatalf("expected 1, got %d", len(errs))
	}
}
