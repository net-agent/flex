package stream

import (
	"io"
	"sync/atomic"
)

// Read 实现 net.Conn。数据来自按字节有界的接收队列，
// ACK 由 ackTracker 合批后异步发送，不再每次 Read 派生 goroutine。
func (s *Stream) Read(dist []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	if len(dist) == 0 {
		return 0, nil
	}
	s.ensureAckFlusher()

	for {
		n, err := s.recv.read(dist)
		if n > 0 {
			atomic.AddInt64(&s.state.BytesRead, int64(n))
			s.acks.add(n)
			return n, nil
		}
		if err != nil {
			return 0, err
		}

		select {
		case <-s.recv.notify:
			// 有新数据，回到循环顶部重新尝试
		case <-s.recv.done:
			// 读侧关闭：回到顶部排空剩余数据，随后返回 io.EOF
		case <-s.readDeadline.Done():
			// Could be a real timeout OR a deadline reset (Set closed old channel).
			// Re-read Done(): if the NEW channel is still closed → real timeout.
			// If the new channel is open → deadline was reset, retry.
			if s.isReadDeadlineExceeded() {
				return 0, ErrTimeout
			}
		}
	}
}

// ensureAckFlusher 惰性启动每条流的 ACK 合并 goroutine。
// 从未发生过 Read 的流不会产生这个 goroutine。
func (s *Stream) ensureAckFlusher() {
	s.ackOnce.Do(func() {
		go s.acks.loop()
	})
}

// isReadDeadlineExceeded performs double-read to distinguish true timeout
// from deadline-reset wakeups (old done channel closed by Set()).
func (s *Stream) isReadDeadlineExceeded() bool {
	select {
	case <-s.readDeadline.Done():
		select {
		case <-s.readDeadline.Done():
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// available 返回当前可读字节数，供测试与观测使用。
func (s *Stream) available() int {
	return s.recv.pending()
}

var _ io.Reader = (*Stream)(nil)
