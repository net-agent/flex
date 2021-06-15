package flex

import (
	"errors"
	"log"
	"net"
	"sync"
)

type Network struct {
	hosts sync.Map // map[HostIP]*Host

	// 分发由host传上来的数据包
	chanPacketRoute chan *Packet
	availableIP     chan HostIP
	macIPBinds      sync.Map // map[string]HostIP
	ipMacBinds      sync.Map // map[HostIP]string
}

func NewNetwork(staticIP map[string]HostIP) *Network {
	n := &Network{
		chanPacketRoute: make(chan *Packet, 1024),
		availableIP:     make(chan HostIP, 0xFFFF),
	}

	if staticIP != nil {
		for k, v := range staticIP {
			n.macIPBinds.Store(k, v)
		}
	}

	// push available ip
	for i := HostIP(1); i < 0xFFFF; i++ {
		_, found := n.ipMacBinds.Load(i)
		if !found {
			n.availableIP <- i
		}
	}

	return n
}

func (n *Network) allocIP(mac string) (HostIP, error) {
	it, found := n.macIPBinds.Load(mac)
	if found {
		return it.(HostIP), nil
	}

	for {
		select {
		case ip, ok := <-n.availableIP:
			if !ok {
				return 0, errors.New("alloc host ip failed")
			}
			if _, found := n.hosts.Load(ip); !found {
				return ip, nil
			}

		default:
			return 0, errors.New("host ip resource depletion")
		}
	}
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
				log.Println("host upgrade failed", err)
				return
			}
			_, loaded := n.hosts.LoadOrStore(host.ip, host)
			if loaded {
				c.Close()
				log.Println("host ip confilct", err)
			}
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
