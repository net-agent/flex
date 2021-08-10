package node

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

func (node *Node) readLoop(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		pbuf, err := node.ReadBuffer()
		if err != nil {
			return
		}

		err = node.routeBuffer(pbuf)
		if err != nil {
			return
		}
	}
}

func (node *Node) routeBuffer(pbuf *packet.Buffer) error {
	select {
	case node.readBufChan <- pbuf:
		return nil
	case <-time.After(DefaultWriteLocalTimeout):
		return errors.New("route buffer timeout")
	}
}
