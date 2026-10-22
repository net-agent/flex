package stream

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestConnReader(t *testing.T) {
	t.Run("read timeout via deadline", func(t *testing.T) {
		s1, _ := Pipe()
		s1.SetReadDeadline(time.Now().Add(testMedTimeout))

		_, err := io.ReadFull(s1, make([]byte, 1))
		assert.Equal(t, ErrTimeout, err)
	})

	t.Run("write timeout via deadline", func(t *testing.T) {
		_, s2 := Pipe()
		s2.window.SetSize(1)
		s2.SetWriteDeadline(time.Now().Add(testMedTimeout))

		_, err := s2.Write([]byte{1, 2, 3})
		assert.ErrorIs(t, err, ErrTimeout)
	})

	t.Run("read after close returns EOF", func(t *testing.T) {
		_, s2 := Pipe()
		err := s2.Close()
		assert.Nil(t, err)

		_, err = s2.Read(make([]byte, 1))
		assert.Equal(t, io.EOF, err)
	})

	t.Run("write to closed remote returns error", func(t *testing.T) {
		s1, s2 := Pipe()
		err := s2.Close()
		assert.Nil(t, err)

		// wait for close propagation
		time.Sleep(testShortTimeout)

		_, err = s1.Write([]byte{1, 2, 3})
		assert.Equal(t, ErrWriterIsClosed, err)
	})
}

func TestReadErr_SendDataAckFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewConnWriter(c)
	s := New(pc, 0)
	pc.SetWriteTimeout(testMedTimeout)

	pushData(s, []byte("12345abcde12345abcde"))
	n, err := s.Read(make([]byte, 10))
	assert.Equal(t, 10, n)
	assert.Nil(t, err)

	// 等待合批窗口到期后 ACK 写超时（只验证不 panic / 不死锁）
	time.Sleep(DefaultAckFlushInterval + testLongTimeout)
}

func TestPartialRead(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("hello world"))

	buf := make([]byte, 5)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), buf)

	// 剩余部分在下一次 Read 返回
	buf2 := make([]byte, 20)
	n, err = s.Read(buf2)
	assert.Nil(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, []byte(" world"), buf2[:n])
}

func TestReadPartialKeepsRemainder(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("abcdef"))

	buf := make([]byte, 3)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []byte("abc"), buf[:n])
	assert.Equal(t, 3, s.available())

	n, err = s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []byte("def"), buf[:n])
}

func TestReadExactFitThenEOFAfterCloseRead(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("abc"))
	pushData(s, []byte("def"))
	assert.Nil(t, s.CloseRead())

	buf := make([]byte, 6)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, []byte("abcdef"), buf[:n])

	n, err = s.Read(buf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
}

func TestReadSkipsEmptyPacketsThenReadsData(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte{})
	pushData(s, []byte{})
	pushData(s, []byte("xyz"))

	buf := make([]byte, 8)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []byte("xyz"), buf[:n])
}

func TestReadOnlyEmptyPacketsThenEOF(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte{})
	pushData(s, []byte{})
	assert.Nil(t, s.CloseRead())

	buf := make([]byte, 8)
	n, err := s.Read(buf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
}

func TestReadTimeoutWhenNoDataAndDeadlineExceeded(t *testing.T) {
	s := New(&mockWriter{}, 0)
	s.SetReadDeadline(time.Now().Add(-time.Second))

	n, err := s.Read(make([]byte, 1))
	assert.Equal(t, 0, n)
	assert.Equal(t, ErrTimeout, err)
}

func TestReadReturnsBufferedDataEvenWhenDeadlineExceeded(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("hello"))
	s.SetReadDeadline(time.Now().Add(-time.Second))

	buf := make([]byte, 8)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), buf[:n])
}

func TestReadBytesReadAccountingAcrossReads(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("ab"))
	pushData(s, []byte("cde"))
	pushData(s, []byte("fg"))
	assert.Nil(t, s.CloseRead())

	buf1 := make([]byte, 4)
	n, err := s.Read(buf1)
	assert.Nil(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, []byte("abcd"), buf1[:n])

	buf2 := make([]byte, 8)
	n, err = s.Read(buf2)
	assert.Nil(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []byte("efg"), buf2[:n])

	n, err = s.Read(buf2)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	st := s.GetState()
	assert.Equal(t, int64(7), st.BytesRead)
}

func TestReadDataIntegritySmallReadsVsLargeRead(t *testing.T) {
	buildStream := func() *Stream {
		s := New(&mockWriter{}, 0)
		pushData(s, []byte("ab"))
		pushData(s, []byte("cd"))
		pushData(s, []byte("ef"))
		assert.Nil(t, s.CloseRead())
		return s
	}

	small := buildStream()
	large := buildStream()

	var smallOut []byte
	smallBuf := make([]byte, 1)
	for {
		n, err := small.Read(smallBuf)
		if n > 0 {
			smallOut = append(smallOut, smallBuf[:n]...)
		}
		if err != nil {
			assert.Equal(t, io.EOF, err)
			break
		}
	}

	largeBuf := make([]byte, 16)
	n, err := large.Read(largeBuf)
	assert.Nil(t, err)
	largeOut := append([]byte(nil), largeBuf[:n]...)
	n, err = large.Read(largeBuf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	assert.Equal(t, []byte("abcdef"), smallOut)
	assert.Equal(t, []byte("abcdef"), largeOut)
}

func TestReadDrainsMultipleQueuedPacketsInSingleCall(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("aa"))
	pushData(s, []byte("bb"))
	pushData(s, []byte("cc"))

	buf := make([]byte, 16)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, []byte("aabbcc"), buf[:n])
}

func TestReadReturnsBufferedDataBeforeEOFInSingleCall(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("aaa"))
	pushData(s, []byte("bbb"))
	assert.Nil(t, s.CloseRead())

	buf := make([]byte, 16)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, []byte("aaabbb"), buf[:n])

	n, err = s.Read(buf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
}

func TestCloseInterruptsBlockedRead(t *testing.T) {
	s := New(&mockWriter{}, 0)

	done := make(chan error, 1)
	go func() {
		_, err := s.Read(make([]byte, 10))
		done <- err
	}()

	time.Sleep(testShortTimeout)
	s.CloseRead()

	select {
	case err := <-done:
		assert.Equal(t, io.EOF, err)
	case <-time.After(testLongTimeout):
		t.Fatal("Read did not unblock after CloseRead")
	}
}

func TestParallelIndependentStreamsRead(t *testing.T) {
	const goroutines = 10
	streams := make([]*Stream, goroutines)
	for i := range streams {
		streams[i] = New(&mockWriter{}, 0)
		pushData(streams[i], []byte("data1"))
		pushData(streams[i], []byte("data2"))
	}

	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(s *Stream) {
			buf := make([]byte, 10)
			n, err := s.Read(buf)
			assert.Nil(t, err)
			assert.Equal(t, 10, n)
			assert.Equal(t, []byte("data1data2"), buf[:n])
			done <- struct{}{}
		}(streams[i])
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func TestReadDrainsBufferedDataAfterCloseRead(t *testing.T) {
	s := New(&mockWriter{}, 0)

	pushData(s, []byte("aaa"))
	pushData(s, []byte("bbb"))
	pushData(s, []byte("ccc"))

	err := s.CloseRead()
	assert.Nil(t, err)

	var total int
	buf := make([]byte, 64)
	for {
		n, err := s.Read(buf)
		total += n
		if err != nil {
			assert.Equal(t, io.EOF, err)
			break
		}
	}
	assert.Equal(t, 9, total, "should drain all buffered data: aaa+bbb+ccc = 9 bytes")
}

func TestReadZeroLengthBuffer(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pushData(s, []byte("hello"))

	n, err := s.Read([]byte{})
	assert.Nil(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 5, s.available())

	buf := make([]byte, 8)
	n, err = s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 5, n)
}

func TestConcurrentProducerConsumer(t *testing.T) {
	s1, s2 := Pipe()

	const totalMessages = 500
	const msgSize = 128
	payload := make([]byte, msgSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	go func() {
		for i := 0; i < totalMessages; i++ {
			s1.Write(payload)
		}
		s1.Close()
	}()

	var totalRead int
	buf := make([]byte, 256)
	for {
		n, err := s2.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
	}

	assert.Equal(t, totalMessages*msgSize, totalRead,
		"consumer should receive all produced data")
}

// 回归 P0：慢消费者 + 小包曾导致接收队列（按包计数，512 个）先于
// 流控窗口（按字节，2MB）耗尽：发送端在 2 秒后被静默断流，
// 已写入的数据永久丢失且发送端无任何错误。
// 新实现按字节计量，1 字节小包在窗口内必须全部送达。
func TestSlowReaderSmallPacketsNoDataLoss(t *testing.T) {
	s1, s2 := Pipe()

	const total = 2000
	writeErr := make(chan error, 1)
	go func() {
		b := []byte{'x'}
		for i := 0; i < total; i++ {
			if _, err := s1.Write(b); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()

	select {
	case err := <-writeErr:
		assert.NoError(t, err, "sender must not fail while data is within the window")
	case <-time.After(3 * time.Second):
		t.Fatal("writer blocked unexpectedly")
	}

	// 消费端此时才开始读（模拟消费者停顿超过旧的 2s 超时）
	buf := make([]byte, 64)
	got := 0
	for got < total {
		n, err := s2.Read(buf)
		assert.NoError(t, err)
		if n == 0 {
			break
		}
		got += n
	}
	assert.Equal(t, total, got, "all small packets must be delivered")
}

// ACK 合批：大量小读不应产生“一读一包”的 ACK 放大。
func TestReadAckBatching(t *testing.T) {
	rec := &recordingWriter{}
	s := New(rec, 0)
	// flusher 在首次 Read 时才启动，此前调整参数是安全的
	s.acks.interval = 20 * time.Millisecond
	s.acks.threshold = 64 * KB
	defer s.CloseRead()

	const packets = 2000
	const size = 256
	payload := make([]byte, size)
	for i := 0; i < packets; i++ {
		pushData(s, payload)
	}

	buf := make([]byte, 512)
	read := 0
	for read < packets*size {
		n, err := s.Read(buf)
		assert.NoError(t, err)
		read += n
	}

	// 等待尾批 ACK flush
	time.Sleep(s.acks.interval * 4)

	rec.mu.Lock()
	ackPackets := 0
	ackBytes := 0
	for _, h := range rec.bufs {
		if h[0] == packet.AckPushStreamData {
			ackPackets++
			ackBytes += int(h[9])<<8 | int(h[10])
		}
	}
	rec.mu.Unlock()

	assert.Equal(t, packets*size, ackBytes, "all consumed bytes must be acked")
	assert.Less(t, ackPackets, 30, "ACKs must be batched, got %d packets", ackPackets)
}

// Test that read deadline returns a proper timeout error satisfying net.Error
func TestReadDeadlineReturnsNetError(t *testing.T) {
	s := New(&mockWriter{}, 0)
	s.SetReadDeadline(time.Now().Add(testMedTimeout))

	_, err := s.Read(make([]byte, 10))
	assert.Equal(t, ErrTimeout, err)

	// Verify it satisfies net.Error interface
	var netErr net.Error
	assert.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout(), "should be a timeout error")
}
