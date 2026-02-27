package stream

import (
	"net"
)

func (s *Stream) LocalAddr() net.Addr  { return &s.state.LocalAddr }
func (s *Stream) RemoteAddr() net.Addr { return &s.state.RemoteAddr }

func (s *Stream) setLocal(ip, port uint16) {
	s.state.LocalAddr.SetIPPort(ip, port)
	s.sender.SetSrc(ip, port)
}
func (s *Stream) setRemote(ip, port uint16) {
	s.state.RemoteAddr.SetIPPort(ip, port)
	s.sender.SetDst(ip, port)
}
func (s *Stream) setNetwork(name string) {
	s.state.LocalAddr.SetNetwork(name)
	s.state.RemoteAddr.SetNetwork(name)
}
