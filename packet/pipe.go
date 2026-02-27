package packet

import "net"

func Pipe() (Conn, Conn) {
	c1, c2 := net.Pipe()
	return NewWithConn(c1), NewWithConn(c2)
}
