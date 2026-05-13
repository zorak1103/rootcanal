package session

import (
	"context"
	"sync"
	"time"
)

// ringBuf is a bounded byte ring buffer. Write never blocks and overwrites
// the oldest bytes when full. A single consumer calls Drain after WaitForData.
type ringBuf struct {
	mu        sync.Mutex
	data      []byte
	start     int  // index of oldest valid byte
	n         int  // number of valid bytes
	cap       int
	truncated bool        // sticky; cleared by Drain
	notify    chan struct{} // 1-buffered; signalled on every Write
}

func newRingBuf(capacity int) *ringBuf {
	if capacity <= 0 {
		capacity = 1 << 20 // 1 MiB fallback
	}
	return &ringBuf{
		data:   make([]byte, capacity),
		cap:    capacity,
		notify: make(chan struct{}, 1),
	}
}

// Write implements io.Writer. Never blocks; overwrites oldest bytes on overflow.
func (r *ringBuf) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	for _, b := range p {
		if r.n == r.cap {
			// Overwrite oldest byte.
			r.data[r.start] = b
			r.start = (r.start + 1) % r.cap
			r.truncated = true
		} else {
			idx := (r.start + r.n) % r.cap
			r.data[idx] = b
			r.n++
		}
	}
	r.mu.Unlock()

	select {
	case r.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

// Drain returns all buffered bytes and resets the buffer.
// Also returns whether bytes were silently dropped since the last Drain.
func (r *ringBuf) Drain() ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.n == 0 {
		wasTrunc := r.truncated
		r.truncated = false
		return nil, wasTrunc
	}
	out := make([]byte, r.n)
	for i := range out {
		out[i] = r.data[(r.start+i)%r.cap]
	}
	r.start = 0
	r.n = 0
	wasTrunc := r.truncated
	r.truncated = false
	return out, wasTrunc
}

// WaitForData blocks until data arrives and a quiesce gap is observed,
// or until maxWait expires, or ctx is cancelled.
func (r *ringBuf) WaitForData(ctx context.Context, quiesce, maxWait time.Duration) {
	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()

	// Phase 1: wait for the first byte.
	select {
	case <-r.notify:
	case <-deadline.C:
		return
	case <-ctx.Done():
		return
	}

	// Phase 2: quiesce — wait until no bytes arrive for `quiesce` duration.
	q := time.NewTimer(quiesce)
	defer q.Stop()
	for {
		select {
		case <-r.notify:
			q.Reset(quiesce)
		case <-q.C:
			return
		case <-deadline.C:
			return
		case <-ctx.Done():
			return
		}
	}
}
