package stream

import (
	"errors"
	"net"
	"time"
)

var (
	ErrWriterIsClosed      = errors.New("writer is closed")
	ErrReaderIsClosed      = errors.New("reader is closed")
	ErrWaitCloseAckTimeout = errors.New("wait close ack timeout")
)

func (s *Stream) Close() error {
	// 1. Ensure absolute resource cleanup locally
	defer s.recv.discard() // 应用主动关闭：未读数据不再承诺可读，归还缓冲池
	defer s.CloseRead()

	// 2. Mark Write as closed locally
	if err := s.CloseWrite(); err != nil {
		// Second call to Close: return net.ErrClosed per net.Conn contract (P3 fix)
		if errors.Is(err, ErrWriterIsClosed) {
			return net.ErrClosed
		}
		return err // defer will run CloseRead
	}

	// 3. Notify Remote
	if err := s.sender.SendClose(); err != nil {
		return err // defer will run CloseRead
	}

	// 4. Wait for confirmation (Graceful period)
	select {
	case <-s.closeAckCh:
		return nil
	case <-time.After(s.closeAckTimeout):
		return ErrWaitCloseAckTimeout // defer will run CloseRead
	}
}

func (s *Stream) CloseRead() error {
	return s.closeRead(false)
}

// closeRead 关闭读方向。
//
// discard=true 表示应用/节点主动放弃读取：未消费的接收数据直接归还缓冲池
// （与 net.Conn 语义一致，Close 后不再承诺可读）。
// discard=false 用于对端主动发送 Close 的场景：对端可能"先发数据、后发 Close"，
// 缓冲区中尚未读走的数据必须保留，由应用继续读取直到 EOF。
func (s *Stream) closeRead(discard bool) error {
	s.rchanMu.Lock()
	if s.readClosed {
		s.rchanMu.Unlock()
		return ErrReaderIsClosed
	}
	s.readClosed = true
	s.rchanMu.Unlock()

	s.recv.close()
	s.acks.stop()
	if discard {
		s.recv.discard()
	}

	// 本地主动关闭默认记为 "local"；若已被预设（remote/node/manual）则保持首次赋值
	s.SetCloseReason("local")
	s.markReadClosed()

	return nil
}

// CloseWrite sets the write state to closed and signals blocked Write operations
// to wake up immediately via closeCh.
func (s *Stream) CloseWrite() error {
	s.writeMu.Lock()

	if s.writeClosed {
		s.writeMu.Unlock()
		return ErrWriterIsClosed
	}

	s.writeClosed = true
	s.stateMu.Lock()
	s.state.Closed = time.Now()
	s.state.IsClosed = true
	s.stateMu.Unlock()

	// Signal blocked Write to wake up immediately (P1 fix)
	close(s.closeCh)
	s.writeMu.Unlock()

	// 本地主动关闭默认记为 "local"；若已被预设（remote/node/manual）则保持首次赋值
	s.SetCloseReason("local")

	// Best effort: also interrupt an in-flight blocking send.
	s.sender.InterruptWrite()
	s.markWriteClosed()

	return nil
}
