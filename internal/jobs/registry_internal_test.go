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
		age        time.Duration
		wantExists bool
	}{
		{"exactly at ttl survives", ttl, true},
		{"one nanosecond past ttl is reaped", ttl + time.Nanosecond, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Within a synctest bubble the fake clock does not advance
				// between this call and the time.Now() inside Reap, so
				// now.Sub(finished) == tt.age exactly.
				finished := time.Now().Add(-tt.age)
				r := &Registry{
					jobs: map[string]*Job{
						"j": {ID: "j", finishedAt: &finished},
					},
					ttl: ttl,
				}

				r.Reap()

				if _, ok := r.jobs["j"]; ok != tt.wantExists {
					t.Errorf("job present after Reap = %v, want %v", ok, tt.wantExists)
				}
			})
		})
	}
}
