package stream

import (
	"bytes"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test high-concurrency Write with continuous ACK refill under backpressure.
func TestWriteHighConcurrencyWithBackpressure(t *testing.T) {
	sw := &sizingWriter{}
	s := New(sw, 256)

	const (
		goroutines  = 64
		payloadSize = 32 * 1024
	)
	payload := bytes.Repeat([]byte{0x2A}, payloadSize)

	stopAck := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopAck:
				return
			case <-ticker.C:
				s.window.Release(256)
			}
		}
	}()
	defer close(stopAck)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, goroutines)
	var totalWritten int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			wn, err := s.Write(payload)
			if err != nil {
				errCh <- err
				return
			}
			atomic.AddInt64(&totalWritten, int64(wn))
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	expected := int64(goroutines * payloadSize)
	assert.Equal(t, expected, atomic.LoadInt64(&totalWritten))
	assert.Equal(t, int(expected), sw.totalBytes())

	_, bytesWritten := s.GetReadWriteSize()
	assert.Equal(t, expected, bytesWritten)
}

// Test that many blocked writers are woken up quickly after CloseWrite.
func TestCloseWriteUnblocksManyBlockedWriters(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	s.window.SetSize(0)

	const writers = 128
	start := make(chan struct{})
	results := make(chan error, writers)

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Write([]byte("blocked"))
			results <- err
		}()
	}

	close(start)
	time.Sleep(testShortTimeout)

	require.NoError(t, s.CloseWrite())

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked writers were not unblocked by CloseWrite")
	}

	close(results)
	for err := range results {
		assert.Equal(t, ErrWriterIsClosed, err)
	}
}

// Test write path stability under frequent deadline changes with blocked writers.
func TestBlockedWritesWithDeadlineThrash(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	s.window.SetSize(0)

	const (
		writers       = 48
		setterWorkers = 8
		setIterations = 400
	)

	start := make(chan struct{})
	results := make(chan error, writers)

	var writeWG sync.WaitGroup
	for i := 0; i < writers; i++ {
		writeWG.Add(1)
		go func() {
			defer writeWG.Done()
			<-start
			_, err := s.Write([]byte("x"))
			results <- err
		}()
	}

	var setWG sync.WaitGroup
	stopSet := make(chan struct{})
	for i := 0; i < setterWorkers; i++ {
		setWG.Add(1)
		go func(seed int) {
			defer setWG.Done()
			for j := 0; j < setIterations; j++ {
				select {
				case <-stopSet:
					return
				default:
				}
				switch (j + seed) % 3 {
				case 0:
					_ = s.SetWriteDeadline(time.Now().Add(2 * time.Millisecond))
				case 1:
					_ = s.SetWriteDeadline(time.Time{})
				default:
					_ = s.SetWriteDeadline(time.Now().Add(-time.Millisecond))
				}
				runtime.Gosched()
			}
		}(i)
	}

	close(start)
	time.Sleep(testShortTimeout)
	_ = s.CloseWrite()
	close(stopSet)
	setWG.Wait()

	done := make(chan struct{})
	go func() {
		writeWG.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked writers did not finish under deadline thrash + close")
	}

	close(results)
	for err := range results {
		if err != ErrWriterIsClosed && err != ErrTimeout {
			t.Fatalf("unexpected write error: %v", err)
		}
	}
}

// Extreme scenario: payload is much larger than split size while window is tiny.
func TestWriteLargePayloadWithTinyWindowAndAckPump(t *testing.T) {
	sw := &sizingWriter{}
	s := New(sw, 128)

	payload := make([]byte, DefaultSplitSize*3+777)
	for i := range payload {
		payload[i] = byte(i)
	}

	require.NoError(t, s.SetWriteDeadline(time.Now().Add(5*time.Second)))

	stopAck := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopAck:
				return
			default:
				s.window.Release(128)
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	wn, err := s.Write(payload)
	close(stopAck)

	require.NoError(t, err)
	assert.Equal(t, len(payload), wn)
	assert.Equal(t, len(payload), sw.totalBytes())

	for _, sz := range sw.getSizes() {
		assert.LessOrEqual(t, sz, DefaultSplitSize)
	}
}
