package flex

import (
	"fmt"

	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/stream"
	"github.com/net-agent/flex/v2/switcher"
)

type FlexDemo struct {
	name string
	info string
}

func (fd *FlexDemo) String() string {
	return fmt.Sprintf("%v %v", fd.name, fd.info)
}

type Node = node.Node
type Stream = stream.Conn
type Switcher = switcher.Server
