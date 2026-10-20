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

// SetCloseReason 记录流的关闭原因，首次赋值生效（first-wins），后续调用被忽略。
// 原因仅在流完全关闭后具有观测意义，但允许在关闭前预设
// （如节点关闭、手动关闭场景先预设原因再走 CloseRead/CloseWrite）。
// 本地主动关闭路径（Close/CloseRead/CloseWrite）会默认记录 "local"，
// 已被预设的原因不会被其覆盖。
func (s *Stream) SetCloseReason(reason string) {
	s.stateMu.Lock()
	if s.state.CloseReason == "" {
		s.state.CloseReason = reason
	}
	s.stateMu.Unlock()
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
