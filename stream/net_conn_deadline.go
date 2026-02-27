package stream

import (
	"sync"
	"time"
)

// timeoutError implements net.Error for deadline-exceeded errors.
// This satisfies the net.Conn contract that timeout errors must
// return true for both Timeout() and Temporary().
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// ErrTimeout is the sentinel timeout error returned when a deadline expires.
var ErrTimeout error = &timeoutError{}

// neverCloseCh is a shared channel that is never closed.
// Used by Done() when no deadline is set, avoiding per-call allocation.
var neverCloseCh = make(chan struct{})

// DeadlineGuard manages a deadline for Read or Write operations.
//
// It uses a channel-based approach: when the deadline expires, the internal
// doneCh is closed, which unblocks any select waiting on Done(). This allows
// Read/Write to detect timeout without permanently closing the stream.
//
// Key properties:
//   - Reversible: Setting a new deadline after expiry creates a new channel,
//     allowing the stream to be used again.
//   - Past time: Setting a deadline in the past immediately closes the channel.
//   - Zero time: Cancels the deadline (Done() returns a never-closing channel).
//   - Interrupt: Setting a new deadline closes the old channel, waking up any
//     blocked select. Callers must re-check Done() to distinguish a real
//     timeout from a deadline reset.
type DeadlineGuard struct {
	mu     sync.Mutex
	doneCh chan struct{} // closed when deadline expires or replaced; nil means no deadline
	timer  *time.Timer
}

// Done returns a channel that is closed when the deadline expires.
// If no deadline is set, returns a shared never-closing channel.
// This method is safe to call concurrently.
func (guard *DeadlineGuard) Done() <-chan struct{} {
	guard.mu.Lock()
	ch := guard.doneCh
	guard.mu.Unlock()
	if ch == nil {
		return neverCloseCh
	}
	return ch
}

// Set updates the deadline. The behavior depends on the value of t:
//   - Zero time: cancels any existing deadline. Done() will return a never-closing channel.
//   - Past time: immediately expires. Done() channel is closed right away.
//   - Future time: starts a timer. Done() channel will be closed when the timer fires.
//
// Each call to Set closes the previous channel (if any), waking up blocked
// selects. Callers should re-check Done() after waking to distinguish a real
// timeout from a deadline reset.
func (guard *DeadlineGuard) Set(t time.Time) {
	guard.mu.Lock()
	defer guard.mu.Unlock()

	// Stop any existing timer
	if guard.timer != nil {
		guard.timer.Stop()
		guard.timer = nil
	}

	// Close old channel to wake up any blocked select.
	// This is safe even if the channel was already closed (e.g., expired deadline),
	// because we use closeSafe.
	guard.closePrev()

	if t.IsZero() {
		// Cancel deadline: nil doneCh → Done() returns neverCloseCh
		guard.doneCh = nil
		return
	}

	// Create a new channel for this deadline
	ch := make(chan struct{})
	guard.doneCh = ch

	dur := time.Until(t)
	if dur <= 0 {
		// Past time: immediately expire
		close(ch)
		return
	}

	// Future time: start timer to close channel when deadline arrives
	guard.timer = time.AfterFunc(dur, func() {
		guard.mu.Lock()
		defer guard.mu.Unlock()
		// Only close if this is still the current channel
		// (Set may have been called again in the meantime)
		if guard.doneCh == ch {
			close(ch)
		}
	})
}

// closePrev closes the previous doneCh if it exists and hasn't been closed yet.
// Must be called with guard.mu held.
func (guard *DeadlineGuard) closePrev() {
	if guard.doneCh == nil {
		return
	}
	select {
	case <-guard.doneCh:
		// Already closed, nothing to do
	default:
		close(guard.doneCh)
	}
}

func (s *Stream) SetDeadline(t time.Time) error {
	s.readDeadline.Set(t)
	s.writeDeadline.Set(t)
	return nil
}

func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline.Set(t)
	return nil
}

func (s *Stream) SetReadDeadline(t time.Time) error {
	s.readDeadline.Set(t)
	return nil
}
