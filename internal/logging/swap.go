// Package logging provides a swappable slog.Handler whose underlying
// implementation can be replaced atomically at runtime.
package logging

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// Swappable is a slog.Handler that delegates to an atomically-replaceable
// inner handler. Swap can be called concurrently with logging.
type Swappable struct {
	inner atomic.Pointer[slog.Handler]
}

// New returns a Swappable initialised with h as the inner handler.
func New(h slog.Handler) *Swappable {
	s := &Swappable{}
	s.inner.Store(&h)
	return s
}

// Swap atomically replaces the inner handler with h.
func (s *Swappable) Swap(h slog.Handler) {
	s.inner.Store(&h)
}

func (s *Swappable) Enabled(ctx context.Context, level slog.Level) bool {
	return (*s.inner.Load()).Enabled(ctx, level)
}

func (s *Swappable) Handle(ctx context.Context, r slog.Record) error {
	return (*s.inner.Load()).Handle(ctx, r)
}

func (s *Swappable) WithAttrs(attrs []slog.Attr) slog.Handler {
	return (*s.inner.Load()).WithAttrs(attrs)
}

func (s *Swappable) WithGroup(name string) slog.Handler {
	return (*s.inner.Load()).WithGroup(name)
}
