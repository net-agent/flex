package stream

import (
	"errors"
	"net"
)

func (s *Stream) LocalAddr() net.Addr  { return &s.local }
func (s *Stream) RemoteAddr() net.Addr { return &s.remote }

func (s *Stream) GetUsedPort() (uint16, error) {
	if s.isDialer {
		return s.local.Port(), nil
	}
	return s.local.Port(), errors.New("local port still on listen")
}

func (s *Stream) SetLocal(ip, port uint16) {
	s.local.SetIPPort(ip, port)
	s.Sender.SetSrc(ip, port)
}
func (s *Stream) SetRemote(ip, port uint16) {
	s.remote.SetIPPort(ip, port)
	s.Sender.SetDist(ip, port)
}
func (s *Stream) SetNetwork(name string) {
	s.local.SetNetwork(name)
	s.remote.SetNetwork(name)
}
