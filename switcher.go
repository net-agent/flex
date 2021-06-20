package flex

import (
	"errors"
	"log"
	"net"
	"sync"

	"github.com/net-agent/cipherconn"
)

type switchContext struct {
	host   *Host
	ip     HostIP
	domain string
	mac    string
}

func (ctx *switchContext) attach(sw *Switcher) {
	sw.domainCtxs.Store(ctx.domain, ctx)
	sw.ctxs.Store(ctx.ip, ctx)
}

func (ctx *switchContext) detach(sw *Switcher) {
	sw.domainCtxs.Delete(ctx.domain)
	sw.ctxs.Delete(ctx.ip)
}

// Switcher packet交换器，根据ip、port进行路由和分发
type Switcher struct {
	password   string
	ctxs       sync.Map // map[HostIP]*switchContext
	domainCtxs sync.Map // map[string]*switchContext

	// 分发由host传上来的数据包
	chanPushData chan *PacketBufs // 用于保证Push的顺序和性能，同时避免writePool死锁
	availableIP  chan HostIP
	macIPBinds   sync.Map // map[mac string]HostIP
	ipMacBinds   sync.Map // map[HostIP]string

}

func NewSwitcher(staticIP map[string]HostIP, password string) *Switcher {
	switcher := &Switcher{
		password:     password,
		availableIP:  make(chan HostIP, 0xFFFF),
		chanPushData: make(chan *PacketBufs, 2048), // 缓冲区需要足够长
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

	go switcher.pushDataLoop()
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
			if _, found := switcher.ctxs.Load(ip); !found {
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
	if switcher.password != "" {
		cc, err := cipherconn.New(conn, switcher.password)
		if err != nil {
			conn.Close()
			return
		}
		conn = cc
	}
	ctx, err := switcher.UpgradeHost(conn)
	if err != nil {
		conn.Close()
		log.Println("host upgrade failed", err)
		return
	}

	ctx.attach(switcher) // detach on error
	log.Printf("host upgrade success, ip is %v\n", ctx.ip)
}

//
// hostReadLoop
// 不断读取连接中的数据，并根据DistIP对数据进行分发
//
func (switcher *Switcher) hostReadLoop(host *Host) {
	for {
		pb := NewPacketBufs()
		_, err := pb.ReadFrom(host.conn)
		if err != nil {
			host.emitReadErr(err)
			return
		}

		if pb.head.Cmd() == CmdAlive {
			continue
		}

		// log.Printf("%v %v\n", pb.head.CmdStr(), pb.head)

		switch pb.head[0] {
		case CmdOpenStreamDomain:
			go switcher.switchOpenDomain(pb)
		case CmdPushStreamData:
			switcher.chanPushData <- pb
		default:
			go switcher.switchData(pb)
		}
	}
}

func (switcher *Switcher) switchOpenDomain(pb *PacketBufs) {
	domain := string(pb.payload)
	it, found := switcher.domainCtxs.Load(domain)
	if !found {
		log.Printf("resolve domain failed: '%v' not found", domain)
		return
	}
	ctx := it.(*switchContext)
	pb.head[0] = CmdOpenStream
	pb.SetDistIP(ctx.ip)
	pb.SetPayload(nil)
	ctx.host.writeBuffer(pb.head[:], pb.payload)
}

func (switcher *Switcher) pushDataLoop() {

	for pb := range switcher.chanPushData {
		switcher.switchData(pb)
	}
}

func (switcher *Switcher) switchData(pb *PacketBufs) {
	// write pb to dist hosts
	it, found := switcher.ctxs.Load(pb.head.DistIP())
	if !found {
		log.Printf("%v%v -> %v discard\n", pb.head.CmdStr(), pb.head.Src(), pb.head.Dist())
		return
	}

	err := it.(*switchContext).host.writeBuffer(pb.head[:], pb.payload)
	if err != nil {
		log.Printf("%v%v -> %v write failed. %v\n",
			pb.head.CmdStr(), pb.head.Src(), pb.head.Dist(), err)
	}
}
