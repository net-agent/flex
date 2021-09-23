package node

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

func (node *Node) readLoop(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		pbuf, err := node.ReadBuffer()
		if err != nil {
			if node.WaitRecover() != nil {
				return
			}
			continue
		}

		err = node.routeBuffer(pbuf)
		if err != nil {
			// 该错误不致命，直接丢包处理即可
			log.Printf("route buffer err: %v\n", err)
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
