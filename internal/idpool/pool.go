package idpool

import (
	"errors"
	"sync"
)

var (
	ErrPoolExhausted = errors.New("id pool exhausted")
	ErrOutOfRange    = errors.New("id out of range")
	ErrNotAllocated  = errors.New("id not allocated")
	ErrInvalidRange  = errors.New("invalid pool range: require min <= max")
)

// Pool is a thread-safe uint16 ID allocator.
// IDs are lazily allocated from [min, max] and recycled via a free stack.
type Pool struct {
	mu   sync.Mutex
	used map[uint16]struct{}
	free []uint16
	next uint32 // uint32 to avoid wrap-around at 0xFFFF
	min  uint16
	max  uint16
}

// New creates a Pool that allocates IDs in [min, max].
func New(min, max uint16) (*Pool, error) {
	if min > max {
		return nil, ErrInvalidRange
	}
	return &Pool{
		used: make(map[uint16]struct{}),
		next: uint32(min),
		min:  min,
		max:  max,
	}, nil
}

// Allocate returns an unused ID. It advances the next counter first
// for sequential allocation, then falls back to recycled IDs.
// Returns ErrPoolExhausted when all IDs in the range are in use.
func (p *Pool) Allocate() (uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Prefer sequential allocation.
	if p.next <= uint32(p.max) {
		id := uint16(p.next)
		p.next++
		p.used[id] = struct{}{}
		return id, nil
	}

	// Fall back to recycled IDs.
	if n := len(p.free); n > 0 {
		id := p.free[n-1]
		p.free = p.free[:n-1]
		p.used[id] = struct{}{}
		return id, nil
	}

	return 0, ErrPoolExhausted
}

// Release returns a previously allocated ID to the pool.
func (p *Pool) Release(id uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if id < p.min || id > p.max {
		return ErrOutOfRange
	}
	if _, ok := p.used[id]; !ok {
		return ErrNotAllocated
	}

	delete(p.used, id)
	p.free = append(p.free, id)
	return nil
}

// InUse returns the number of currently allocated IDs.
func (p *Pool) InUse() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.used)
}
