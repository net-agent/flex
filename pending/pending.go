package pending

import (
	"errors"
	"sync"
)

var (
	ErrAlreadyRegistered = errors.New("key already registered")
	ErrNotFound          = errors.New("key not found")
	ErrTimeout           = errors.New("wait reply timeout")
)

type Result[T any] struct {
	Val T
	Err error
}

type Requests[T any] struct {
	mu      sync.Mutex
	waiters map[uint16]chan Result[T]
}

func (r *Requests[T]) Register(key uint16) (<-chan Result[T], error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.waiters == nil {
		r.waiters = make(map[uint16]chan Result[T])
	}

	if _, ok := r.waiters[key]; ok {
		return nil, ErrAlreadyRegistered
	}

	ch := make(chan Result[T], 1)
	r.waiters[key] = ch
	return ch, nil
}

func (r *Requests[T]) Complete(key uint16, val T, err error) error {
	r.mu.Lock()
	ch, ok := r.waiters[key]
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	delete(r.waiters, key)
	r.mu.Unlock()

	ch <- Result[T]{Val: val, Err: err}
	return nil
}

func (r *Requests[T]) Remove(key uint16) {
	r.mu.Lock()
	ch, ok := r.waiters[key]
	if ok {
		delete(r.waiters, key)
		close(ch)
	}
	r.mu.Unlock()
}
