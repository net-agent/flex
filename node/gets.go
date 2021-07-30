package node

import (
	"errors"
	"time"
)

func (node *Node) GetIP() uint16 {
	return node.ip
}

func (node *Node) GetFreePort() (uint16, error) {
	for {
		select {
		case port := <-node.freePorts:
			_, loaded := node.listenPorts.Load(port)
			if loaded {
				// 此端口被listener占用
				continue
			}
			return port, nil
		case <-time.After(time.Second):
			return 0, errors.New("free port dry")
		}
	}
}
