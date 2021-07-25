package stream

import (
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
	return nil
}

func (s *Conn) RemoteAddr() net.Addr {
	return nil
}
