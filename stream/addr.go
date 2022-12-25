package stream

import "fmt"

type Addr struct {
	NetworkName string
	str         string
	IP          uint16
	Port        uint16
}

func (a *Addr) String() string  { return a.str }
func (a *Addr) Network() string { return a.NetworkName }

func (a *Addr) SetNetwork(name string) { a.NetworkName = name }
func (a *Addr) SetIPPort(ip, port uint16) {
	a.str = fmt.Sprintf("%v:%v", ip, port)
	a.IP = ip
	a.Port = port
}
