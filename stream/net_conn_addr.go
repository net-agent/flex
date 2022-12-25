package stream

import (
	"net"
)

func (s *Stream) LocalAddr() net.Addr  { return &s.state.LocalAddr }
func (s *Stream) RemoteAddr() net.Addr { return &s.state.RemoteAddr }

func (s *Stream) SetLocal(ip, port uint16) {
	s.state.LocalAddr.SetIPPort(ip, port)
	s.Sender.SetSrc(ip, port)
}
func (s *Stream) SetRemote(ip, port uint16) {
	s.state.RemoteAddr.SetIPPort(ip, port)
	s.Sender.SetDist(ip, port)
}
func (s *Stream) SetNetwork(name string) {
	s.state.LocalAddr.SetNetwork(name)
	s.state.RemoteAddr.SetNetwork(name)
}
