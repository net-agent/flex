package stream

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

type MockWriter struct{}

func (m *MockWriter) WriteBuffer(buf *packet.Buffer) error { return nil }
func (m *MockWriter) SetWriteTimeout(dur time.Duration)    {}

func TestCloseLeakOnTimeout(t *testing.T) {
	s := New(&MockWriter{}, 0)
	// Reduce timeout for testing
	s.closeAckTimeout = time.Millisecond * 100

	// Start a consumer that reads
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for {
			_, err := s.Read(buf)
			if err != nil {
				// We expect an error (EOF or closed)
				return
			}
		}
	}()

	// Perform Close.
	// Since MockWriter doesn't reply with ACK, this WILL timeout.
	startTime := time.Now()
	err := s.Close()
	dur := time.Since(startTime)

	if err == nil {
		t.Error("expected timeout error, got nil")
	}

	// Verify durability
	if dur < time.Millisecond*100 {
		t.Errorf("expected close to wait for timeout, but returned in %v", dur)
	}

	// MOST IMPORTANT: Verify consumer unblocked
	select {
	case <-done:
		// Success: Read unblocked meaning bytesChan was closed
	case <-time.After(time.Millisecond * 200):
		t.Error("consumer goroutine leaked! Read() is still blocked")
	}
}

func TestConcurrentClose(t *testing.T) {
	s := New(&MockWriter{}, 0)
	s.closeAckTimeout = time.Millisecond * 50

	// Simulate concurrent close
	go s.Close()
	go s.Close()

	// Simulate concurrent passive close
	go s.HandleCmdCloseStream(packet.NewBuffer(nil))

	// Simulate late ACK
	go func() {
		time.Sleep(time.Millisecond * 10)
		s.HandleAckCloseStream(packet.NewBuffer(nil))
	}()

	time.Sleep(time.Millisecond * 200)
	// If no panic, we good.
}
