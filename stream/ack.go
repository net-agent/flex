package stream

import (
	"log/slog"
	"sync"
	"time"
)

var (
	// DefaultAckFlushInterval 是 ACK 的合并窗口：小于阈值的零散 ACK
	// 最多等待这么久再发送，用于把小读合并成一个 ACK。
	DefaultAckFlushInterval = 10 * time.Millisecond

	// DefaultAckFlushThreshold 是立即触发 ACK 的字节阈值。
	// 达到阈值立即 flush，保证大流量下发送端窗口能被及时补充。
	DefaultAckFlushThreshold = int64(64 * KB)
)

// ackTracker 负责把 Read 产生的 ACK 合批后异步发送。
//
// 旧实现每次 Read 都 go func(){ SendDataAck(...) }()：
//   - 每次读一个 goroutine，小读场景下 goroutine 数量随读次数线性增长；
//   - 每次读一个 11 字节 ACK 包，4 字节读会放大成 3 倍以上的控制流量。
//
// 新实现每条流最多一个常驻 flusher goroutine（首次 Read 时惰性启动），
// 按“字节阈值或时间窗口”合并发送，流关闭时退出。
type ackTracker struct {
	mu      sync.Mutex
	pending int64
	armed   bool // flusher 已被唤醒、正在等待时间窗口或阈值
	closed  bool

	wake      chan struct{}
	done      chan struct{}
	send      func(uint16) error
	onSent    func(int64)
	logger    *slog.Logger
	interval  time.Duration
	threshold int64
}

func newAckTracker(send func(uint16) error, onSent func(int64), logger *slog.Logger) *ackTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &ackTracker{
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
		send:      send,
		onSent:    onSent,
		logger:    logger,
		interval:  DefaultAckFlushInterval,
		threshold: DefaultAckFlushThreshold,
	}
}

func (a *ackTracker) add(n int) {
	if n <= 0 {
		return
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.pending += int64(n)
	needWake := !a.armed || a.pending >= a.threshold
	if needWake {
		a.armed = true
	}
	a.mu.Unlock()
	if needWake {
		a.signal()
	}
}

func (a *ackTracker) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// take 取走全部待确认字节，并标记 flusher 回到未武装状态。
func (a *ackTracker) take() int64 {
	a.mu.Lock()
	n := a.pending
	a.pending = 0
	a.armed = false
	a.mu.Unlock()
	return n
}

func (a *ackTracker) pendingBytes() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending
}

// loop 是每条流唯一的 ACK 发送 goroutine。
func (a *ackTracker) loop() {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	timerArmed := false
	stopTimer := func() {
		if !timerArmed {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerArmed = false
	}
	defer stopTimer()

	for {
		select {
		case <-a.done:
			// 读侧已关闭：待确认字节不再有意义，直接退出。
			return

		case <-a.wake:
			if a.pendingBytes() == 0 {
				// 迟到的唤醒（flush 期间又有 add 触发），无需再定时
				stopTimer()
				continue
			}
			if a.pendingBytes() >= a.threshold {
				stopTimer()
				a.flush()
				continue
			}
			if !timerArmed {
				timer.Reset(a.interval)
				timerArmed = true
			}

		case <-timer.C:
			timerArmed = false
			a.flush()
		}
	}
}

func (a *ackTracker) flush() {
	n := a.take()
	if n <= 0 {
		return
	}

	remaining := n
	for remaining > 0 {
		chunk := remaining
		if chunk > 0xFFFF {
			chunk = 0xFFFF
		}
		if err := a.send(uint16(chunk)); err != nil {
			a.logger.Warn("SendDataAck failed", "error", err.Error())
			return
		}
		if a.onSent != nil {
			a.onSent(chunk)
		}
		remaining -= chunk
	}
}

// stop 关闭 flusher。幂等，可由 CloseRead 与流清理并发调用。
func (a *ackTracker) stop() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.pending = 0
	a.mu.Unlock()
	close(a.done)
}
