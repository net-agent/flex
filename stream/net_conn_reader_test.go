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

	// simulate buffered data
	s.readBuf = []byte("12345abcde12345abcde")
	n, err := s.Read(make([]byte, 10))
	assert.Equal(t, 10, n)
	assert.Nil(t, err)

	// wait for async ack write to timeout and log error
	time.Sleep(testMedTimeout + testLongTimeout)
}

func TestPartialRead(t *testing.T) {
	s := New(&mockWriter{}, 0)
	data := []byte("hello world")
	s.readBuf = data

	// Read with a small buffer — only 5 bytes
	buf := make([]byte, 5)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("hello"), buf)

	// Read remaining from currBuf
	buf2 := make([]byte, 20)
	n, err = s.Read(buf2)
	assert.Nil(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, []byte(" world"), buf2[:n])
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
	// concurrent Read on the SAME stream is not safe because currBuf
	// is unprotected. This test verifies concurrent reads on SEPARATE streams.
	const goroutines = 10
	streams := make([]*Stream, goroutines)
	for i := range streams {
		streams[i] = New(&mockWriter{}, 0)
		streams[i].recvQueue <- []byte("data1")
		streams[i].recvQueue <- []byte("data2")
	}

	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(s *Stream) {
			buf := make([]byte, 10)
			s.Read(buf)
			s.Read(buf)
			done <- struct{}{}
		}(streams[i])
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func TestReadDrainsBufferedDataAfterCloseRead(t *testing.T) {
	s := New(&mockWriter{}, 0)

	s.recvQueue <- []byte("aaa")
	s.recvQueue <- []byte("bbb")
	s.recvQueue <- []byte("ccc")

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
	s.readBuf = []byte("hello")

	n, err := s.Read([]byte{})
	assert.Nil(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, []byte("hello"), s.readBuf)
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

func TestReadAckGoroutinePressure(t *testing.T) {
	s1, s2 := Pipe()

	const totalMessages = 5000
	const msgSize = 64
	payload := make([]byte, msgSize)

	go func() {
		for i := 0; i < totalMessages; i++ {
			s1.Write(payload)
		}
		s1.Close()
	}()

	buf := make([]byte, 256)
	var totalRead int
	for {
		n, err := s2.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
	}

	assert.Equal(t, totalMessages*msgSize, totalRead)
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
