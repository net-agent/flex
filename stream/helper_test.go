package stream

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

// mockWriter is a no-op packet.Writer for tests that don't need real I/O.
type mockWriter struct{}

func (m *mockWriter) WriteBuffer(buf *packet.Buffer) error { return nil }
func (m *mockWriter) SetWriteTimeout(dur time.Duration)    {}

// recordingWriter records each WriteBuffer call for verification.
type recordingWriter struct {
	mu   sync.Mutex
	bufs []packet.Header
}

func (r *recordingWriter) WriteBuffer(buf *packet.Buffer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := buf.Head
	r.bufs = append(r.bufs, h)
	return nil
}

func (r *recordingWriter) SetWriteTimeout(dur time.Duration) {}

func (r *recordingWriter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bufs)
}

// isReadClosed safely reads s.readClosed under the read channel mutex.
func isReadClosed(s *Stream) bool {
	s.rchanMu.RLock()
	defer s.rchanMu.RUnlock()
	return s.readClosed
}

// isWriteClosed safely reads s.writeClosed under the write mutex.
func isWriteClosed(s *Stream) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writeClosed
}

// sizingWriter records the payload size of each WriteBuffer call.
type sizingWriter struct {
	mu    sync.Mutex
	sizes []int
}

func (w *sizingWriter) WriteBuffer(buf *packet.Buffer) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sizes = append(w.sizes, int(buf.PayloadSize()))
	return nil
}

func (w *sizingWriter) SetWriteTimeout(dur time.Duration) {}

func (w *sizingWriter) getSizes() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]int, len(w.sizes))
	copy(cp, w.sizes)
	return cp
}

func (w *sizingWriter) totalBytes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, s := range w.sizes {
		total += s
	}
	return total
}

// Common test timeouts
const (
	testShortTimeout = 50 * time.Millisecond
	testMedTimeout   = 100 * time.Millisecond
	testLongTimeout  = 200 * time.Millisecond
)

// failOnCmdWriter returns an error when WriteBuffer is called with a specific command.
type failOnCmdWriter struct {
	failCmd byte
}

func (w *failOnCmdWriter) WriteBuffer(buf *packet.Buffer) error {
	if buf.Cmd() == w.failCmd {
		return errors.New("simulated write failure")
	}
	return nil
}

func (w *failOnCmdWriter) SetWriteTimeout(dur time.Duration) {}
