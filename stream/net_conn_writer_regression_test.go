package stream

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateWriter blocks WriteBuffer until Unblock is called.
// Used to force Stream.Write into the "sending stage" blocked state.
type gateWriter struct {
	enterOnce sync.Once
	unblkOnce sync.Once
	mu        sync.Mutex
	timeout   time.Duration
	closed    atomic.Bool
	entered   chan struct{}
	unblock   chan struct{}
}

func newGateWriter() *gateWriter {
	return &gateWriter{
		entered: make(chan struct{}),
		unblock: make(chan struct{}),
	}
}

func (w *gateWriter) WriteBuffer(buf *packet.Buffer) error {
	w.enterOnce.Do(func() { close(w.entered) })
	w.mu.Lock()
	timeout := w.timeout
	w.mu.Unlock()

	if timeout <= 0 {
		<-w.unblock
		if w.closed.Load() {
			return ErrWriterIsClosed
		}
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.unblock:
		if w.closed.Load() {
			return ErrWriterIsClosed
		}
		return nil
	case <-timer.C:
		return ErrTimeout
	}
}

func (w *gateWriter) SetWriteTimeout(dur time.Duration) {
	w.mu.Lock()
	w.timeout = dur
	w.mu.Unlock()
}

func (w *gateWriter) InterruptWrite() {
	w.closed.Store(true)
	w.Unblock()
}

func (w *gateWriter) Unblock() {
	w.unblkOnce.Do(func() { close(w.unblock) })
}

// P1 regression: write deadline should interrupt a send-stage blocked Write.
// This uses gateWriter (test double) with isolated interrupt semantics; it does
// not imply default shared-conn writer supports per-stream send interruption.
func TestWriteDeadlineInterruptsBlockingSend(t *testing.T) {
	gw := newGateWriter()
	s := New(gw, 1024)
	if !s.sender.CanInterruptWrite() {
		t.Skip("known limitation: shared conn mode does not support per-stream send interruption")
	}
	require.NoError(t, s.SetWriteDeadline(time.Now().Add(80*time.Millisecond)))

	done := make(chan error, 1)
	gotResult := false
	defer func() {
		gw.Unblock()
		if !gotResult {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("cleanup failed: blocked Write did not exit")
			}
		}
	}()

	go func() {
		_, err := s.Write([]byte("payload"))
		done <- err
	}()

	select {
	case <-gw.entered:
	case <-time.After(testMedTimeout):
		t.Fatal("Write did not enter sending stage")
	}

	select {
	case err := <-done:
		gotResult = true
		assert.Equal(t, ErrTimeout, err, "Write should return timeout while send is blocked")
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Write did not return after write deadline expired during blocked send")
	}
}

// P1 regression: CloseWrite should not be blocked by an in-flight blocked send.
// This uses gateWriter (test double) with isolated interrupt semantics.
func TestCloseWriteInterruptsBlockingSend(t *testing.T) {
	gw := newGateWriter()
	s := New(gw, 1024)
	if !s.sender.CanInterruptWrite() {
		t.Skip("known limitation: shared conn mode does not support per-stream send interruption")
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte("payload"))
		writeDone <- err
	}()

	select {
	case <-gw.entered:
	case <-time.After(testMedTimeout):
		t.Fatal("Write did not enter sending stage")
	}

	closeDone := make(chan error, 1)
	closeReturned := false
	writeReturned := false
	defer func() {
		gw.Unblock()
		if !closeReturned {
			select {
			case <-closeDone:
			case <-time.After(time.Second):
				t.Fatalf("cleanup failed: CloseWrite did not return")
			}
		}
		if !writeReturned {
			select {
			case <-writeDone:
			case <-time.After(time.Second):
				t.Fatalf("cleanup failed: Write did not return")
			}
		}
	}()

	go func() {
		closeDone <- s.CloseWrite()
	}()

	select {
	case err := <-closeDone:
		closeReturned = true
		require.NoError(t, err, "CloseWrite should return promptly even when send is blocked")
	case <-time.After(300 * time.Millisecond):
		t.Fatal("CloseWrite blocked behind a send-stage blocked Write")
	}

	select {
	case err := <-writeDone:
		writeReturned = true
		assert.Equal(t, ErrWriterIsClosed, err, "blocked Write should be interrupted by CloseWrite")
	case <-time.After(300 * time.Millisecond):
		t.Fatal("blocked Write was not interrupted by CloseWrite")
	}
}

// P2 regression: when stream is already closed, Write should deterministically
// return ErrWriterIsClosed even if deadline is also expired.
func TestWriteClosedBeatsDeadlineDeterministically(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := New(&mockWriter{}, 1024)
		require.NoError(t, s.SetWriteDeadline(time.Now().Add(-time.Millisecond)))
		require.NoError(t, s.CloseWrite())

		_, err := s.Write([]byte("x"))
		require.Equal(t, ErrWriterIsClosed, err, "iteration=%d", i)
	}
}

// P2 regression: ACK release should never increase window above the initial limit.
func TestAckReleaseShouldBeCappedToInitialWindow(t *testing.T) {
	s := New(&mockWriter{}, 100)
	initial := s.window.Available()

	ack := packet.NewBufferWithCmd(packet.AckPushStreamData)
	ack.SetDataACKSize(500)
	s.HandleAckPushStreamData(ack)

	assert.Equal(t, initial, s.window.Available(),
		"ACK without outstanding data should not increase window beyond initial size")
}

// P2 regression: after partial consume, oversized ACK should at most restore to initial window.
func TestAckOversizeShouldNotOverRestoreWindow(t *testing.T) {
	s := New(&mockWriter{}, 100)

	wn, err := s.Write(make([]byte, 80))
	require.NoError(t, err)
	require.Equal(t, 80, wn)
	require.Equal(t, int32(20), s.window.Available())

	ack := packet.NewBufferWithCmd(packet.AckPushStreamData)
	ack.SetDataACKSize(200)
	s.HandleAckPushStreamData(ack)

	assert.Equal(t, int32(100), s.window.Available(),
		"oversized ACK should not restore window beyond initial size")
}
