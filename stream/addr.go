package stream

import (
	"errors"
	"fmt"
	"net"
)

//
// todo
//

type addr struct {
	network string
	str     string
}

func (a *addr) Network() string { return a.network }
func (a *addr) String() string  { return a.str }

func (s *Conn) SetLocal(ip, port uint16) {
	s.localIP = ip
	s.localPort = port
	s.local.str = fmt.Sprintf("%v:%v", ip, port)
}

func (s *Conn) SetRemote(ip, port uint16) {
	s.remoteIP = ip
	s.remotePort = port
	s.remote.str = fmt.Sprintf("%v:%v", ip, port)
}

func (s *Conn) LocalAddr() net.Addr {
	return &s.local
}

func (s *Conn) RemoteAddr() net.Addr {
	return &s.remote
}

func (s *Conn) GetUsedPort() (uint16, error) {
	if s.isDialer {
		return s.localPort, nil
	}
	return s.localPort, errors.New("local port still on listen")
}
