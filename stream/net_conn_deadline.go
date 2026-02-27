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
type DeadlineGuard struct {
	mu     sync.Mutex
	doneCh chan struct{} // closed when deadline expires; nil means no deadline
	timer  *time.Timer
}

// Done returns a channel that is closed when the deadline expires.
// If no deadline is set, returns a channel that never closes.
// This method is safe to call concurrently.
func (guard *DeadlineGuard) Done() <-chan struct{} {
	guard.mu.Lock()
	ch := guard.doneCh
	guard.mu.Unlock()
	if ch == nil {
		return make(chan struct{}) // never closes
	}
	return ch
}

// Set updates the deadline. The behavior depends on the value of t:
//   - Zero time: cancels any existing deadline. Done() will return a never-closing channel.
//   - Past time: immediately expires. Done() channel is closed right away.
//   - Future time: starts a timer. Done() channel will be closed when the timer fires.
//
// Each call to Set creates a new channel, making the deadline reversible.
// After a deadline expires, calling Set with a future time restores usability.
func (guard *DeadlineGuard) Set(t time.Time) {
	guard.mu.Lock()
	defer guard.mu.Unlock()

	// Stop any existing timer
	if guard.timer != nil {
		guard.timer.Stop()
		guard.timer = nil
	}

	if t.IsZero() {
		// Cancel deadline: create nil doneCh (Done() will return never-closing channel)
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
