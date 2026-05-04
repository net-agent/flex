package stream

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestWriteFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewConnWriter(c)
	s := New(pc, 0)
	timeout := time.Millisecond * 100

	pc.SetWriteTimeout(timeout)

	_, err := s.Write(nil)
	assert.Nil(t, err)

	_, err = s.write([]byte{1, 2, 3, 4, 5})
	assert.NotNil(t, err)
}

func TestFlowControl_BucketBlockAndResume(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	s.window.SetSize(0)

	done := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte("hello"))
		done <- err
	}()

	// Write should be blocked; simulate ack arrival
	time.Sleep(testShortTimeout)
	select {
	case <-done:
		t.Fatal("Write should be blocked when window is 0")
	default:
	}

	// Restore window and signal event
	s.window.SetSize(1024)
	s.window.Signal()

	select {
	case err := <-done:
		assert.Nil(t, err)
	case <-time.After(testLongTimeout):
		t.Fatal("Write did not resume after window refill")
	}
}

func TestCloseWriteThenWrite(t *testing.T) {
	s := New(&mockWriter{}, 0)
	err := s.CloseWrite()
	assert.Nil(t, err)

	_, err = s.Write([]byte("data"))
	assert.Equal(t, ErrWriterIsClosed, err)
}

func TestConcurrentWrite(t *testing.T) {
	s := New(&mockWriter{}, 0)
	var wg sync.WaitGroup
	const goroutines = 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Write([]byte("concurrent"))
		}()
	}
	wg.Wait()
}

func TestWriteSplitsLargePayload(t *testing.T) {
	sw := &sizingWriter{}
	s := New(sw, int32(1*MB))

	payload := make([]byte, DefaultSplitSize+1000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	wn, err := s.Write(payload)
	assert.Nil(t, err)
	assert.Equal(t, len(payload), wn)

	sizes := sw.getSizes()
	assert.GreaterOrEqual(t, len(sizes), 2, "should split into at least 2 chunks")
	assert.Equal(t, DefaultSplitSize, sizes[0])
	assert.Equal(t, len(payload), sw.totalBytes())
}

func TestWriteSplitsByBucketSize(t *testing.T) {
	sw := &sizingWriter{}
	s := New(sw, 200)

	payload := make([]byte, 200)
	wn, err := s.Write(payload)
	assert.Nil(t, err)
	assert.Equal(t, 200, wn)

	// window exhausted, write should block. Use deadline to timeout.
	s.window.SetSize(0)
	s.SetWriteDeadline(time.Now().Add(testMedTimeout))

	_, err = s.Write([]byte("more"))
	assert.Equal(t, ErrTimeout, err)
}

func TestWriteSplitBucketPartial(t *testing.T) {
	sw := &sizingWriter{}
	s := New(sw, 100)

	done := make(chan struct{})
	go func() {
		defer close(done)
		wn, err := s.Write(make([]byte, 200))
		assert.Nil(t, err)
		assert.Equal(t, 200, wn)
	}()

	// Wait for first chunk
	time.Sleep(testShortTimeout)
	sizes := sw.getSizes()
	assert.Equal(t, 1, len(sizes), "should have written first chunk")
	assert.Equal(t, 100, sizes[0])

	// Simulate ACK to restore window
	s.window.SetSize(100)
	s.window.Signal()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Write did not complete after window refill")
	}

	assert.Equal(t, 200, sw.totalBytes())
}

func TestWriteEmptyBuf(t *testing.T) {
	s := New(&mockWriter{}, 0)
	n, err := s.Write([]byte{})
	assert.Nil(t, err)
	assert.Equal(t, 0, n)
}

func TestFlowControlWithConcurrentAck(t *testing.T) {
	sw := &sizingWriter{}
	const windowSize int32 = 512
	s := New(sw, windowSize)

	totalToWrite := 4096
	payload := make([]byte, totalToWrite)

	stopAck := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopAck:
				return
			case <-time.After(5 * time.Millisecond):
				s.window.Release(256)
			}
		}
	}()

	wn, err := s.Write(payload)
	close(stopAck)

	assert.Nil(t, err)
	assert.Equal(t, totalToWrite, wn)
	assert.Equal(t, totalToWrite, sw.totalBytes())
}

func TestWriteInterruptedByRemoteClose(t *testing.T) {
	s1, _ := Pipe()

	s1.window.SetSize(512)

	payload := make([]byte, 4096)

	var writeErr error
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, writeErr = s1.Write(payload)
	}()

	// Wait for first chunk, then simulate remote Close
	time.Sleep(testShortTimeout)
	closePbuf := packet.NewBufferWithCmd(packet.CmdCloseStream)
	s1.HandleCmdCloseStream(closePbuf)

	select {
	case <-writeDone:
		assert.NotNil(t, writeErr, "Write should fail after remote close")
		assert.True(t, isReadClosed(s1), "rclosed should be true")
		assert.True(t, isWriteClosed(s1), "wclosed should be true")
	case <-time.After(2 * time.Second):
		t.Fatal("Write did not unblock after remote close")
	}
}

func TestWriteBoundaryValues(t *testing.T) {
	tests := []struct {
		name           string
		payloadSize    int
		expectedChunks int
		firstChunkMax  int
	}{
		{
			name:           "exactly DefaultSplitSize",
			payloadSize:    DefaultSplitSize,
			expectedChunks: 1,
			firstChunkMax:  DefaultSplitSize,
		},
		{
			name:           "DefaultSplitSize + 1",
			payloadSize:    DefaultSplitSize + 1,
			expectedChunks: 2,
			firstChunkMax:  DefaultSplitSize,
		},
		{
			name:           "exactly MaxPayloadSize",
			payloadSize:    packet.MaxPayloadSize,
			expectedChunks: (packet.MaxPayloadSize + DefaultSplitSize - 1) / DefaultSplitSize,
			firstChunkMax:  DefaultSplitSize,
		},
		{
			name:           "MaxPayloadSize + 1",
			payloadSize:    packet.MaxPayloadSize + 1,
			expectedChunks: (packet.MaxPayloadSize + 1 + DefaultSplitSize - 1) / DefaultSplitSize,
			firstChunkMax:  DefaultSplitSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw := &sizingWriter{}
			s := New(sw, int32(2*MB))

			payload := make([]byte, tt.payloadSize)
			wn, err := s.Write(payload)
			assert.Nil(t, err)
			assert.Equal(t, tt.payloadSize, wn)

			sizes := sw.getSizes()
			assert.Equal(t, tt.expectedChunks, len(sizes), "unexpected chunk count")
			assert.LessOrEqual(t, sizes[0], tt.firstChunkMax, "first chunk too large")
			for _, sz := range sizes {
				assert.LessOrEqual(t, sz, DefaultSplitSize, "chunk exceeds split size")
				assert.LessOrEqual(t, sz, packet.MaxPayloadSize, "chunk exceeds payload max")
			}
			assert.Equal(t, tt.payloadSize, sw.totalBytes(), "total bytes mismatch")
		})
	}
}

// Test that write deadline returns proper timeout error
func TestWriteDeadlineReturnsNetError(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	s.window.SetSize(0)
	s.SetWriteDeadline(time.Now().Add(testMedTimeout))

	_, err := s.Write([]byte("blocked"))
	assert.Equal(t, ErrTimeout, err)

	var netErr net.Error
	assert.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout(), "should be a timeout error")
}

type seqChunkWriter struct {
	mu      sync.Mutex
	markers []byte
	delay   time.Duration
}

func (w *seqChunkWriter) WriteBuffer(buf *packet.Buffer) error {
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	w.mu.Lock()
	if len(buf.Payload) > 0 {
		w.markers = append(w.markers, buf.Payload[0])
	}
	w.mu.Unlock()
	return nil
}

func (w *seqChunkWriter) SetWriteTimeout(dur time.Duration) {}

func (w *seqChunkWriter) getMarkers() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.markers))
	copy(out, w.markers)
	return out
}

func TestWriteSerializesConcurrentCallsByInvocationOrder(t *testing.T) {
	sw := &seqChunkWriter{delay: time.Millisecond}
	s := New(sw, int32(4*MB))

	payloadA := bytes.Repeat([]byte{'A'}, DefaultSplitSize+8)
	payloadB := bytes.Repeat([]byte{'B'}, DefaultSplitSize+8)

	done1 := make(chan error, 1)
	done2 := make(chan error, 1)
	started1 := make(chan struct{})

	go func() {
		close(started1)
		_, err := s.Write(payloadA)
		done1 <- err
	}()
	<-started1
	go func() {
		_, err := s.Write(payloadB)
		done2 <- err
	}()

	assert.Nil(t, <-done1)
	assert.Nil(t, <-done2)

	assert.Equal(t, []byte{'A', 'A', 'B', 'B'}, sw.getMarkers())
}

func TestWriteFastConcurrentWritesCompleteWithoutOrderGuarantee(t *testing.T) {
	sw := &seqChunkWriter{delay: time.Millisecond}
	s := New(sw, int32(4*MB))

	payloadA := bytes.Repeat([]byte{'A'}, DefaultSplitSize+8)
	payloadB := bytes.Repeat([]byte{'B'}, DefaultSplitSize+8)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, err := s.WriteFast(payloadA)
		assert.Nil(t, err)
		assert.Equal(t, len(payloadA), n)
	}()
	go func() {
		defer wg.Done()
		n, err := s.WriteFast(payloadB)
		assert.Nil(t, err)
		assert.Equal(t, len(payloadB), n)
	}()
	wg.Wait()

	markers := sw.getMarkers()
	assert.Equal(t, 4, len(markers))
	assert.Equal(t, 2, bytes.Count(markers, []byte{'A'}))
	assert.Equal(t, 2, bytes.Count(markers, []byte{'B'}))
}
