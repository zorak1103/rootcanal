package jobs

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestRegistry_Reap_TTLBoundary(t *testing.T) {
	const ttl = time.Hour
	tests := []struct {
		name       string
		running    bool
		age        time.Duration
		wantExists bool
	}{
		{name: "running job is never reaped", running: true, age: 100 * ttl, wantExists: true},
		{name: "exactly at ttl survives", age: ttl, wantExists: true},
		{name: "one nanosecond past ttl is reaped", age: ttl + time.Nanosecond, wantExists: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Within a synctest bubble the fake clock does not advance
				// between this call and the time.Now() inside Reap, so
				// now.Sub(finished) == tt.age exactly. The Registry is built
				// as a literal rather than via NewRegistry because that
				// constructor starts reaperLoop, whose ticker would advance
				// the bubble's fake clock (breaking the exact-boundary
				// equality above) and would still be running when the
				// bubble body returns, which synctest treats as an error.
				at := time.Now().Add(-tt.age)
				job := &Job{ID: "j", StartedAt: at}
				if !tt.running {
					job.finishedAt = &at
				}
				r := &Registry{
					jobs: map[string]*Job{"j": job},
					ttl:  ttl,
				}

				r.Reap()

				if _, ok := r.jobs["j"]; ok != tt.wantExists {
					t.Errorf("job present after Reap = %v, want %v", ok, tt.wantExists)
				}
			})
		})
	}
}
