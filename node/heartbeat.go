package node

import (
	"sync/atomic"
	"time"
)

type Heartbeat struct {
	host          *Node
	lastWriteTime atomic.Int64 // unix nano
	lastRTTNano   atomic.Int64 // 最近一次探活 RTT（nano）
	interval      time.Duration
	checker       func() error
}

func (h *Heartbeat) init(host *Node, interval time.Duration) {
	h.host = host
	h.lastWriteTime.Store(time.Now().UnixNano())
	h.interval = interval
}

// Touch 更新最后写入时间
func (h *Heartbeat) Touch() {
	h.lastWriteTime.Store(time.Now().UnixNano())
}

// SetChecker 设置探活回调
func (h *Heartbeat) SetChecker(fn func() error) {
	h.checker = fn
}

func (h *Heartbeat) run(ticker *time.Ticker, done <-chan struct{}, closeFunc func()) {
	for {
		select {
		case <-done:
			return
		case _, ok := <-ticker.C:
			if !ok {
				return
			}
		}

		last := h.lastWriteTime.Load()
		if time.Since(time.Unix(0, last)) < h.interval {
			continue
		}

		if h.checker == nil {
			h.host.logger.Warn("aliveChecker is nil")
			return
		}

		err := h.checker()
		if err != nil {
			atomic.AddInt64(&h.host.failures.HeartbeatFailed, 1)
			h.host.trace.heartbeatFail(err)
			h.host.logger.Warn("check alive failed", "error", err)
			closeFunc()
			return
		}
	}
}
