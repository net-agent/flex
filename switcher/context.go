package switcher

import (
	"github.com/net-agent/flex/packet"
)

type Context struct {
	Name   string
	Domain string
	IP     uint16
	Conn   packet.Conn
}
