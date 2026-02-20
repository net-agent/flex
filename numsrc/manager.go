package numsrc

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidNumberRange   = errors.New("invalid number range")
	ErrFreeNumberChClosed   = errors.New("free number chan closed")
	ErrGetFreeNumberTimeout = errors.New("get free number timeout")
	ErrNumberStateNotFound  = errors.New("number state not found")
	ErrInvalidNumberState   = errors.New("invalid number state")
	ErrFreeNumberChOverflow = errors.New("free number chan overflow")
)

type Manager struct {
	min       uint16
	max       uint16
	states    map[uint16]bool
	locker    sync.Mutex
	freeStart uint16
	freeChan  chan uint16
}

// 端口取值范围[min, max]
func NewManager(min, freeStart, max uint16) (*Manager, error) {
	if !(min <= freeStart && freeStart < max) {
		return nil, ErrInvalidNumberRange
	}

	m := &Manager{
		min:       min,
		max:       max,
		states:    make(map[uint16]bool),
		freeStart: freeStart,
		freeChan:  make(chan uint16, max-freeStart),
	}
	for i := freeStart; i < max; i++ {
		m.freeChan <- i
	}

	return m, nil
}

// GetNumberSrc 申请制定编号的端口，返回：是否成功
func (pm *Manager) GetNumberSrc(num uint16) error {
	pm.locker.Lock()
	defer pm.locker.Unlock()
	return pm.requestNumber(num)
}

func (pm *Manager) requestNumber(port uint16) error {
	if port < pm.min || port > pm.max {
		return ErrInvalidNumberRange
	}
	used, found := pm.states[port]
	if found && used {
		return ErrNumberStateNotFound
	}

	pm.states[port] = true
	return nil
}

func (pm *Manager) GetFreeNumberSrc() (uint16, error) {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case port, ok := <-pm.freeChan:
			if !ok {
				return 0, ErrFreeNumberChClosed
			}

			pm.locker.Lock()
			err := pm.requestNumber(port)
			pm.locker.Unlock()
			if err == nil {
				return port, nil
			}

		case <-timer.C:
			return 0, ErrGetFreeNumberTimeout
		}
	}
}

func (pm *Manager) ReleaseNumberSrc(port uint16) error {
	if port < pm.min || port > pm.max {
		return ErrInvalidNumberRange
	}
	pm.locker.Lock()
	defer pm.locker.Unlock()

	used, found := pm.states[port]
	if !found {
		return ErrNumberStateNotFound
	}
	if !used {
		return ErrInvalidNumberState
	}

	pm.states[port] = false

	// 特殊段的需要放入队列中
	if port >= pm.freeStart && port < pm.max {
		select {
		case pm.freeChan <- port:
			return nil
		default:
			return ErrFreeNumberChOverflow
		}
	}

	return nil
}
