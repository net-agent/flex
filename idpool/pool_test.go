package idpool

import (
	"sync"
	"testing"
)

func TestNewValidRange(t *testing.T) {
	p, err := New(1, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.InUse() != 0 {
		t.Fatalf("expected 0 in use, got %d", p.InUse())
	}
}

func TestNewSingleID(t *testing.T) {
	p, err := New(5, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id, err := p.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 5 {
		t.Fatalf("expected 5, got %d", id)
	}
	_, err = p.Allocate()
	if err != ErrPoolExhausted {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
}

func TestNewInvalidRange(t *testing.T) {
	_, err := New(100, 99)
	if err != ErrInvalidRange {
		t.Fatalf("expected ErrInvalidRange, got %v", err)
	}
}

func TestAllocateSequential(t *testing.T) {
	p, _ := New(10, 12)

	for _, want := range []uint16{10, 11, 12} {
		got, err := p.Allocate()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != want {
			t.Fatalf("expected %d, got %d", want, got)
		}
	}

	_, err := p.Allocate()
	if err != ErrPoolExhausted {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
}

func TestReleaseAndReuse(t *testing.T) {
	p, _ := New(1, 3)

	id1, _ := p.Allocate() // 1
	id2, _ := p.Allocate() // 2

	if err := p.Release(id1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// next=3 is still available, so sequential allocation returns 3 first.
	got, err := p.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 3 {
		t.Fatalf("expected sequential 3, got %d", got)
	}

	// Now next is exhausted, recycled id1 is returned.
	got, err = p.Allocate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != id1 {
		t.Fatalf("expected recycled %d, got %d", id1, got)
	}

	_ = id2
}

func TestReleaseErrors(t *testing.T) {
	p, _ := New(10, 20)

	// Out of range.
	if err := p.Release(9); err != ErrOutOfRange {
		t.Fatalf("expected ErrOutOfRange, got %v", err)
	}
	if err := p.Release(21); err != ErrOutOfRange {
		t.Fatalf("expected ErrOutOfRange, got %v", err)
	}

	// Not allocated.
	if err := p.Release(15); err != ErrNotAllocated {
		t.Fatalf("expected ErrNotAllocated, got %v", err)
	}

	// Double release.
	id, _ := p.Allocate()
	p.Release(id)
	if err := p.Release(id); err != ErrNotAllocated {
		t.Fatalf("expected ErrNotAllocated on double release, got %v", err)
	}
}

func TestInUse(t *testing.T) {
	p, _ := New(0, 9)

	ids := make([]uint16, 5)
	for i := range ids {
		ids[i], _ = p.Allocate()
	}
	if p.InUse() != 5 {
		t.Fatalf("expected 5, got %d", p.InUse())
	}

	p.Release(ids[0])
	p.Release(ids[2])
	if p.InUse() != 3 {
		t.Fatalf("expected 3, got %d", p.InUse())
	}
}

func TestConcurrentAllocateRelease(t *testing.T) {
	p, _ := New(0, 0xFFFF)
	const goroutines = 100
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				id, err := p.Allocate()
				if err != nil {
					return
				}
				p.Release(id)
			}
		}()
	}
	wg.Wait()
}
