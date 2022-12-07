package port

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidPortRange    = errors.New("invalid port range")
	ErrFreePortChClosed    = errors.New("free ports chan closed")
	ErrGetFreePortTimeout  = errors.New("get free port timeout")
	ErrPortStateNotFound   = errors.New("port state not found")
	ErrInvalidPortState    = errors.New("invalid port state")
	ErrFreePortsChOverflow = errors.New("free ports chan overflow")
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
		return nil, ErrInvalidPortRange
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

// GetPort 申请制定编号的端口，返回：是否成功
func (pm *Manager) GetPort(port uint16) error {
	pm.locker.Lock()
	defer pm.locker.Unlock()
	return pm.requestPortByNumber(port)
}

func (pm *Manager) requestPortByNumber(port uint16) error {
	if port < pm.min || port > pm.max {
		return ErrInvalidPortRange
	}
	used, found := pm.states[port]
	if found && used {
		return ErrPortStateNotFound
	}

	pm.states[port] = true
	return nil
}

func (pm *Manager) GetFreePort() (uint16, error) {
	pm.locker.Lock()
	defer pm.locker.Unlock()

	for {
		select {
		case port, ok := <-pm.freeChan:
			if !ok {
				return 0, ErrFreePortChClosed
			}

			// 尝试调用requestPortByNumber验证port的可用性，返回true则代表申请成功
			if nil == pm.requestPortByNumber(port) {
				return port, nil
			}

		case <-time.After(time.Second):
			return 0, ErrGetFreePortTimeout
		}
	}
}

func (pm *Manager) ReleasePort(port uint16) error {
	if port < pm.min || port > pm.max {
		return ErrInvalidPortRange
	}
	pm.locker.Lock()
	defer pm.locker.Unlock()

	used, found := pm.states[port]
	if !found {
		return ErrPortStateNotFound
	}
	if !used {
		return ErrInvalidPortState
	}

	pm.states[port] = false

	// 特殊段的需要放入队列中
	if port >= pm.freeStart && port < pm.max {
		select {
		case pm.freeChan <- port:
			return nil
		default:
			return ErrFreePortsChOverflow
		}
	}

	return nil
}
