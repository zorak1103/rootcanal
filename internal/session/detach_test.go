package session

import "testing"

// ---- resolveDetachTimeout tests (Bug #18) ----

func TestResolveDetachTimeout_DefaultsToMax(t *testing.T) {
	// No caller-requested timeout → use the detach max cap.
	got := resolveDetachTimeout(0, 86400000)
	if got != 86400000 {
		t.Errorf("resolveDetachTimeout(0, 86400000) = %d, want 86400000", got)
	}
}

func TestResolveDetachTimeout_CallerRequestedWithinCap(t *testing.T) {
	// Caller asks for 2 min — honour it, don't clamp.
	got := resolveDetachTimeout(120000, 86400000)
	if got != 120000 {
		t.Errorf("resolveDetachTimeout(120000, 86400000) = %d, want 120000", got)
	}
}

func TestResolveDetachTimeout_CallerRequestedExceedsCap(t *testing.T) {
	// Caller asks for > max — clamp to max.
	const oneWeekMs = 7 * 24 * 60 * 60 * 1000
	got := resolveDetachTimeout(oneWeekMs, 86400000)
	if got != 86400000 {
		t.Errorf("resolveDetachTimeout(%d, 86400000) = %d, want 86400000", oneWeekMs, got)
	}
}

func TestResolveDetachTimeout_ZeroMaxMeansNoClamp(t *testing.T) {
	// maxMs=0 means "no cap configured" — should use the requested value or
	// the compiled-in fallback (never block on 0ms).
	got := resolveDetachTimeout(5000, 0)
	if got <= 0 {
		t.Errorf("resolveDetachTimeout(5000, 0) = %d, want positive", got)
	}
}

func TestResolveDetachTimeout_BothZeroUsesFallback(t *testing.T) {
	// Neither caller nor config provide a value → safety fallback (24 h).
	got := resolveDetachTimeout(0, 0)
	const fallback = 24 * 60 * 60 * 1000
	if got != fallback {
		t.Errorf("resolveDetachTimeout(0, 0) = %d, want %d (24h fallback)", got, fallback)
	}
}
