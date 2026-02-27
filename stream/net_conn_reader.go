package stream

import (
	"io"
	"sync/atomic"
)

func (s *Stream) Read(dist []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	for len(s.readBuf) == 0 {
		select {
		case buf, ok := <-s.recvQueue:
			if !ok {
				return 0, io.EOF
			}
			s.readBuf = buf

		case <-s.readDeadline.Done():
			// Could be a real timeout OR a deadline reset (Set closed old channel).
			// Re-read Done(): if the NEW channel is still closed → real timeout.
			// If the new channel is open → deadline was reset, retry.
			select {
			case <-s.readDeadline.Done():
				return 0, ErrTimeout
			default:
				continue
			}
		}
	}

	n := copy(dist, s.readBuf)
	atomic.AddInt64(&s.state.BytesRead, int64(n))
	s.readBuf = s.readBuf[n:]
	if n > 0 {
		// 发送ack不能阻塞Read，所以放在协程里
		// 确保发送ack的操作不会因为网络问题而阻塞Read，从而导致连接无法正常关闭或数据无法及时读取
		go func() {
			err := s.sender.SendDataAck(uint16(n))
			if err != nil {
				s.logger.Warn("SendDataAck failed", "error", err.Error())
				return
			}
			atomic.AddInt64(&s.state.SentAckTotal, int64(n))
		}()
	}
	return n, nil
}
