package stream

import "fmt"

type addr struct {
	network string
	str     string
	ip      uint16
	port    uint16
}

func (a *addr) Network() string        { return a.network }
func (a *addr) String() string         { return a.str }
func (a *addr) IP() uint16             { return a.ip }
func (a *addr) Port() uint16           { return a.port }
func (a *addr) SetNetwork(name string) { a.network = name }
func (a *addr) SetIPPort(ip, port uint16) {
	a.str = fmt.Sprintf("%v:%v", ip, port)
	a.ip = ip
	a.port = port
}
