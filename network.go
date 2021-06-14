package flex

import (
	"errors"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

type Network struct {
	hosts       sync.Map // map[ip]*Host
	hostIPIndex uint32

	// 分发由host传上来的数据包
	chanPacketRoute chan *Packet
}

func (n *Network) Run(addr string) {
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatal(err)
		return
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		go func(c net.Conn) {
			host, err := n.upgrade(c)
			if err != nil {
				c.Close()
				log.Println(err)
				return
			}
			n.hosts.Store(atomic.AddUint32(&n.hostIPIndex, 1), host)
		}(conn)
	}
}

func (n *Network) upgrade(conn net.Conn) (*Host, error) {
	return nil, errors.New("not implement")
}

// todo todo todo
func (n *Network) packetRouteLoop() {
	for {
		select {
		case packet, ok := <-n.chanPacketRoute:
			if ok {
				it, found := n.hosts.Load(0)
				if found {
					it.(*Host).chanRead <- packet
				}
			}
		}
	}
}
