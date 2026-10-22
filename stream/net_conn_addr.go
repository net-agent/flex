package stream

import (
	"net"
)

func (s *Stream) LocalAddr() net.Addr  { return &s.state.LocalAddr }
func (s *Stream) RemoteAddr() net.Addr { return &s.state.RemoteAddr }

func (s *Stream) setLocal(ip, port uint16) {
	s.stateMu.Lock()
	s.state.LocalAddr.SetIPPort(ip, port)
	s.stateMu.Unlock()
	s.sender.SetSrc(ip, port)
}
func (s *Stream) setRemote(ip, port uint16) {
	s.stateMu.Lock()
	s.state.RemoteAddr.SetIPPort(ip, port)
	s.stateMu.Unlock()
	s.sender.SetDst(ip, port)
}
func (s *Stream) setNetwork(name string) {
	s.stateMu.Lock()
	s.state.LocalAddr.SetNetwork(name)
	s.state.RemoteAddr.SetNetwork(name)
	s.stateMu.Unlock()
}

// GetWindowAvailable 返回当前剩余的发送窗口（字节），用于观测与测试。
func (s *Stream) GetWindowAvailable() int32 {
	return s.window.Available()
}
