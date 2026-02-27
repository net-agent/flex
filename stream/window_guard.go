package stream

import "sync/atomic"

// WindowGuard encapsulates the flow control window for a stream.
//
// It manages a sliding window of available capacity: the writer consumes
// capacity when sending data, and the reader releases capacity when ACKs
// arrive from the remote peer. An event channel notifies blocked writers
// that capacity has been restored.
type WindowGuard struct {
	size      int32
	maxWindow int32
	event     chan struct{}
}

// NewWindowGuard creates a WindowGuard with the given initial window size
// and event channel capacity.
func NewWindowGuard(initialSize int32, eventChanCap int) *WindowGuard {
	maxWindow := initialSize
	if maxWindow <= 0 {
		// Keep test helpers usable when constructed with 0.
		maxWindow = 1<<31 - 1
	}
	return &WindowGuard{
		size:      initialSize,
		maxWindow: maxWindow,
		event:     make(chan struct{}, eventChanCap),
	}
}

// Available returns the current remaining window capacity.
func (w *WindowGuard) Available() int32 {
	return atomic.LoadInt32(&w.size)
}

// Consume decreases the window by n after sending data.
func (w *WindowGuard) Consume(n int32) {
	atomic.AddInt32(&w.size, -n)
}

// Release increases the window by n when an ACK is received,
// and sends a non-blocking signal on the event channel.
func (w *WindowGuard) Release(n int32) {
	if n <= 0 {
		return
	}
	var changed bool
	for {
		cur := atomic.LoadInt32(&w.size)
		max := atomic.LoadInt32(&w.maxWindow)
		next := cur + n
		if next < cur || next > max {
			next = max
		}
		if atomic.CompareAndSwapInt32(&w.size, cur, next) {
			changed = next > cur
			break
		}
	}
	if !changed {
		return
	}
	select {
	case w.event <- struct{}{}:
	default:
	}
}

// Event returns the channel that signals capacity restoration.
// Writers should select on this channel when the window is exhausted.
func (w *WindowGuard) Event() <-chan struct{} {
	return w.event
}

// SetSize directly sets the window size. For testing only.
func (w *WindowGuard) SetSize(n int32) {
	atomic.StoreInt32(&w.size, n)
	for {
		curMax := atomic.LoadInt32(&w.maxWindow)
		if n <= curMax {
			return
		}
		if atomic.CompareAndSwapInt32(&w.maxWindow, curMax, n) {
			return
		}
	}
}

// Signal manually sends an event. For testing only.
func (w *WindowGuard) Signal() {
	select {
	case w.event <- struct{}{}:
	default:
	}
}
