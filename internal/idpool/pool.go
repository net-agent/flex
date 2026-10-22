package idpool

import (
	"errors"
	"sync"
	"sync/atomic"
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
//
// Allocate 返回裸 ID，调用方必须自行保证"只释放一次"。
// 当释放责任会跨 goroutine/跨超时路径转移时（典型：连接关闭与请求超时竞态），
// 优先使用 Acquire 获取租约，由租约保证幂等释放。
func (p *Pool) Allocate() (uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allocateLocked()
}

func (p *Pool) allocateLocked() (uint16, error) {
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

// Lease 表示一次 ID 占用。与裸 ID 不同，Lease.Release 是幂等的：
// 重复释放不会把已重新分配给其他持有者的 ID 再次归还池子。
//
// 这修复了一类典型缺陷：同一资源的所有权在"请求超时路径"与"连接关闭路径"
// 之间转移时，两边各释放一次，第二次会释放掉别人刚拿到的同号资源。
type Lease struct {
	pool *Pool
	id   uint16
	done atomic.Bool
}

// Acquire 从池中分配一个 ID 并返回租约。
func (p *Pool) Acquire() (*Lease, error) {
	id, err := p.Allocate()
	if err != nil {
		return nil, err
	}
	return &Lease{pool: p, id: id}, nil
}

// ID 返回租约占用的 ID。
func (l *Lease) ID() uint16 {
	if l == nil {
		return 0
	}
	return l.id
}

// Release 归还 ID。幂等且 nil-safe，重复调用返回 nil 且不影响池状态。
func (l *Lease) Release() error {
	if l == nil {
		return nil
	}
	if l.done.Swap(true) {
		return nil
	}
	return l.pool.Release(l.id)
}

// Released 报告租约是否已释放。
func (l *Lease) Released() bool {
	return l == nil || l.done.Load()
}

// Capacity returns the total number of IDs in the pool range [min, max].
// The range is fixed at creation, so no lock is needed.
func (p *Pool) Capacity() int {
	return int(p.max) - int(p.min) + 1
}

// InUse returns the number of currently allocated IDs.
func (p *Pool) InUse() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.used)
}
