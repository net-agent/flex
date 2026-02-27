package stream

import (
	"runtime"
	"sync/atomic"
)

func (s *Stream) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	var wn, bufSize, windowSize int

	for {
		// Wait for window capacity (flow control)
		if s.window.Available() <= 0 {
			select {
			case <-s.window.Event():
				// Window capacity restored, continue
			case <-s.closeCh:
				// Stream is being closed, fail immediately
				return wn, ErrWriterIsClosed
			case <-s.writeDeadline.Done():
				return wn, ErrTimeout
			}
		}

		sliceSize := DefaultSplitSize

		bufSize = len(buf)
		if bufSize < sliceSize {
			sliceSize = bufSize
		}

		windowSize = int(s.window.Available())
		if windowSize < sliceSize {
			sliceSize = windowSize
		}

		n, err := s.write(buf[:sliceSize])
		if n > 0 {
			wn += n
			buf = buf[n:]
		}

		if err != nil {
			return wn, err
		}

		if len(buf) <= 0 {
			return wn, nil
		}

		runtime.Gosched()
	}
}

func (s *Stream) write(buf []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.writeClosed {
		return 0, ErrWriterIsClosed
	}

	err := s.sender.SendData(buf)
	if err != nil {
		return 0, err
	}

	wn := len(buf)

	atomic.AddInt64(&s.state.BytesWritten, int64(wn))
	s.window.Consume(int32(wn))
	return wn, nil
}
