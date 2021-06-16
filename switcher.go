package flex

import (
	"errors"
	"log"
	"net"
	"sync"
)

// Switcher packet交换器，根据ip、port进行路由和分发
type Switcher struct {
	hosts sync.Map // map[HostIP]*Host

	// 分发由host传上来的数据包
	chanPacketBufferRoute chan []byte
	availableIP           chan HostIP
	macIPBinds            sync.Map // map[string]HostIP
	ipMacBinds            sync.Map // map[HostIP]string
}

func NewSwitcher(staticIP map[string]HostIP) *Switcher {
	n := &Switcher{
		chanPacketBufferRoute: make(chan []byte, 1024),
		availableIP:           make(chan HostIP, 0xFFFF),
	}

	for k, v := range staticIP {
		n.macIPBinds.Store(k, v)
		n.ipMacBinds.Store(v, k)
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

func (n *Switcher) allocIP(mac string) (HostIP, error) {
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

func (n *Switcher) Run(addr string) {
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

func (n *Switcher) upgrade(conn net.Conn) (*Host, error) {
	return nil, errors.New("not implement")
}

// todo todo todo
func (n *Switcher) packetSwitchLoop() {
	for {
		select {
		case buf, ok := <-n.chanPacketBufferRoute:
			if ok {
				it, found := n.hosts.Load(0)
				if found {
					it.(*Host).writeBuffer(buf)
				}
			}
		}
	}
}
