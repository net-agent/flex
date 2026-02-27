package stream

const (
	closeMaskRead uint32 = 1 << iota
	closeMaskWrite
	closeMaskBoth = closeMaskRead | closeMaskWrite
)

// SetOnDetach sets a callback invoked once when both read and write are closed.
func (s *Stream) SetOnDetach(fn func()) {
	if fn == nil {
		return
	}
	s.onDetachMu.Lock()
	s.onDetachFn = fn
	s.onDetachMu.Unlock()
	s.tryOnDetach()
}

func (s *Stream) markReadClosed() {
	s.markClosed(closeMaskRead)
}

func (s *Stream) markWriteClosed() {
	s.markClosed(closeMaskWrite)
}

func (s *Stream) markClosed(mask uint32) {
	for {
		old := s.closeMask.Load()
		next := old | mask
		if s.closeMask.CompareAndSwap(old, next) {
			break
		}
	}
	s.tryOnDetach()
}

func (s *Stream) tryOnDetach() {
	if s.closeMask.Load() != closeMaskBoth {
		return
	}
	s.onDetachMu.RLock()
	cb := s.onDetachFn
	s.onDetachMu.RUnlock()
	if cb == nil {
		return
	}
	if !s.detachDone.CompareAndSwap(false, true) {
		return
	}
	cb()
}
