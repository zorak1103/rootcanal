package session

import (
	"strings"
	"testing"
)

func TestCappedBuffer_Write_BelowCap(t *testing.T) {
	cb := &cappedBuffer{cap: 100}
	n, err := cb.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Errorf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if cb.String() != "hello" {
		t.Errorf("String = %q, want %q", cb.String(), "hello")
	}
	if cb.Truncated() {
		t.Error("Truncated should be false")
	}
}

func TestCappedBuffer_Write_ExactCap(t *testing.T) {
	cb := &cappedBuffer{cap: 5}
	cb.Write([]byte("hello"))
	if cb.String() != "hello" {
		t.Errorf("String = %q, want %q", cb.String(), "hello")
	}
	if cb.Truncated() {
		t.Error("Truncated should be false for exactly cap bytes")
	}
}

func TestCappedBuffer_Write_OverCap(t *testing.T) {
	cb := &cappedBuffer{cap: 3}
	n, err := cb.Write([]byte("hello"))
	// The implementation truncates p before writing, so n == remaining (3).
	// err must be nil regardless so the caller doesn't treat it as fatal.
	if n != 3 || err != nil {
		t.Errorf("Write = (%d, %v), want (3, nil)", n, err)
	}
	if cb.String() != "hel" {
		t.Errorf("String = %q, want %q", cb.String(), "hel")
	}
	if !cb.Truncated() {
		t.Error("Truncated should be true")
	}
}

func TestCappedBuffer_Write_AfterFull(t *testing.T) {
	cb := &cappedBuffer{cap: 3}
	cb.Write([]byte("abc"))
	cb.Write([]byte("more")) // all discarded
	if cb.String() != "abc" {
		t.Errorf("String = %q, want %q", cb.String(), "abc")
	}
	if !cb.Truncated() {
		t.Error("Truncated should be true")
	}
}

func TestCappedBuffer_Write_Empty(t *testing.T) {
	cb := &cappedBuffer{cap: 10}
	n, err := cb.Write([]byte{})
	if n != 0 || err != nil {
		t.Errorf("Write([]) = (%d, %v)", n, err)
	}
}

func TestCappedBuffer_Concurrent(t *testing.T) {
	// cappedBuffer must be safe for concurrent writes (SSH library writes from
	// its mux goroutine).
	cb := &cappedBuffer{cap: 1000}
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				cb.Write([]byte(strings.Repeat("x", 5)))
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// Just verify it didn't panic and String/Truncated don't race.
	_ = cb.String()
	_ = cb.Truncated()
}
