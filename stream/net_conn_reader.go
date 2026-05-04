package stream

import (
	"io"
	"math"
	"sync/atomic"
)

func (s *Stream) Read(dist []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	if len(dist) == 0 {
		return 0, nil
	}

	total := 0

	copyFromReadBuf := func() {
		if len(s.readBuf) == 0 || total >= len(dist) {
			return
		}
		n := copy(dist[total:], s.readBuf)
		total += n
		s.readBuf = s.readBuf[n:]
	}

	for total == 0 {
		copyFromReadBuf()
		if total > 0 {
			break
		}

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

	for total < len(dist) {
		copyFromReadBuf()
		if total >= len(dist) || len(s.readBuf) > 0 {
			continue
		}

		select {
		case buf, ok := <-s.recvQueue:
			if !ok {
				goto ACK
			}
			s.readBuf = buf
		default:
			goto ACK
		}
	}

ACK:
	atomic.AddInt64(&s.state.BytesRead, int64(total))
	if total > 0 {
		// TODO(low-priority): ACK is sent in a goroutine per Read call.
		// Under extreme slow-network conditions this can accumulate goroutines.
		// Keep current behavior for simplicity; optimize later if needed.
		go func(n int) {
			remaining := n
			for remaining > 0 {
				chunk := remaining
				if chunk > math.MaxUint16 {
					chunk = math.MaxUint16
				}

				err := s.sender.SendDataAck(uint16(chunk))
				if err != nil {
					s.logger.Warn("SendDataAck failed", "error", err.Error())
					return
				}
				atomic.AddInt64(&s.state.SentAckTotal, int64(chunk))
				remaining -= chunk
			}
		}(total)
	}
	return total, nil
}
