package node

import (
	"log/slog"
	"sync/atomic"
	"time"
)

type Heartbeat struct {
	lastWriteTime int64 // atomic unix nano
	interval      time.Duration
	checker       func() error
	logger        *slog.Logger
}

func (h *Heartbeat) init(host *Node, interval time.Duration) {
	h.lastWriteTime = time.Now().UnixNano()
	h.interval = interval
	h.logger = host.logger
}

// Touch 更新最后写入时间
func (h *Heartbeat) Touch() {
	atomic.StoreInt64(&h.lastWriteTime, time.Now().UnixNano())
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

		last := atomic.LoadInt64(&h.lastWriteTime)
		if time.Since(time.Unix(0, last)) < h.interval {
			continue
		}

		if h.checker == nil {
			h.logger.Warn("aliveChecker is nil")
			return
		}

		err := h.checker()
		if err != nil {
			h.logger.Warn("check alive failed", "error", err)
			closeFunc()
			return
		}
	}
}
