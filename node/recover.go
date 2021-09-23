package node

import (
	"errors"

	"github.com/net-agent/flex/v2/packet"
)

type RecoverFunc func() (packet.Conn, error)

func (node *Node) SetRecover(fn RecoverFunc) {}

func (node *Node) WaitRecover() error {
	return errors.New("recover failed")
}
