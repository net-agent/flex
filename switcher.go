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
	switcher := &Switcher{
		chanPacketBufferRoute: make(chan []byte, 1024),
		availableIP:           make(chan HostIP, 0xFFFF),
	}

	for k, v := range staticIP {
		switcher.macIPBinds.Store(k, v)
		switcher.ipMacBinds.Store(v, k)
	}

	// push available ip
	for i := HostIP(1); i < 0xFFFF; i++ {
		_, found := switcher.ipMacBinds.Load(i)
		if !found {
			switcher.availableIP <- i
		}
	}

	return switcher
}

func (switcher *Switcher) allocIP(mac string) (HostIP, error) {
	it, found := switcher.macIPBinds.Load(mac)
	if found {
		return it.(HostIP), nil
	}

	for {
		select {
		case ip, ok := <-switcher.availableIP:
			if !ok {
				return 0, errors.New("alloc host ip failed")
			}
			if _, found := switcher.hosts.Load(ip); !found {
				return ip, nil
			}

		default:
			return 0, errors.New("host ip resource depletion")
		}
	}
}

func (switcher *Switcher) Run(addr string) {
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatal(err)
		return
	}

	log.Printf("switcher running, addr is %v\n", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		go switcher.Access(conn)
	}
}

func (switcher *Switcher) Access(conn net.Conn) {
	host, ip, err := switcher.UpgradeHost(conn)
	if err != nil {
		conn.Close()
		log.Println("host upgrade failed", err)
		return
	}
	_, loaded := switcher.hosts.LoadOrStore(ip, host)
	if loaded {
		conn.Close()
		log.Printf("host ip(%v) conflict\n", ip)
	}

	log.Printf("host upgrade success, ip is %v\n", ip)
}
