package flex

import (
	"errors"
	"log"
	"net"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/net-agent/cipherconn"
)

type switchContext struct {
	host   *Host
	ip     HostIP
	domain string
	mac    string
}

func (sw *Switcher) attach(ctx *switchContext) {
	sw.domainCtxs.Store(ctx.domain, ctx)
	sw.ctxs.Store(ctx.ip, ctx)
}

func (sw *Switcher) detach(ctx *switchContext) {
	sw.domainCtxs.Delete(ctx.domain)
	sw.ctxs.Delete(ctx.ip)
}

// Switcher packet交换器，根据ip、port进行路由和分发
type Switcher struct {
	password string

	// 分发由host传上来的数据包
	dataChans []chan *PacketBufs // 用于保证Push的顺序和性能，同时避免writePool死锁

	availableIP chan HostIP
	ipMacBinds  sync.Map // map[HostIP]string
	macIPBinds  sync.Map // map[mac string]HostIP

	//
	// context indexs
	//
	ctxs          sync.Map // map[ip HostIP]*switchContext
	domainCtxs    sync.Map // map[domain string]*switchContext
	activeCtxsLen int32
}

func NewSwitcher(staticIP map[string]HostIP, password string) *Switcher {
	switcher := &Switcher{
		password:    password,
		availableIP: make(chan HostIP, 0xFFFF),
	}

	// 初始化数据通道
	switcher.dataChans = make([]chan *PacketBufs, 0)
	for i := 0; i < runtime.NumCPU(); i++ {
		switcher.dataChans = append(switcher.dataChans, make(chan *PacketBufs, 2048))
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

func (switcher *Switcher) selectIP(mac string) (HostIP, error) {
	it, found := switcher.macIPBinds.Load(mac)
	if found {
		return it.(HostIP), nil
	}

	for {
		select {
		case ip, ok := <-switcher.availableIP:
			if !ok {
				return 0, errors.New("select host ip failed")
			}
			if _, found := switcher.ctxs.Load(ip); !found {
				return ip, nil
			}

		default:
			return 0, errors.New("host ip resource depletion")
		}
	}
}

func (switcher *Switcher) ServeWebsocket(wsconn *websocket.Conn) {
	defer wsconn.Close()
	remote := wsconn.RemoteAddr().String()

	ctx, err := switcher.UpgradeToContext(NewWsPacketConn(wsconn))
	if err != nil {
		log.Printf("host(remote=%v) upgrade failed: %v\n", remote, err)
		return
	}
	switcher.ServeContext(ctx, remote)
}

func (switcher *Switcher) Run(addr string) {
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatal(err)
		return
	}

	switcher.Serve(l)
}

func (switcher *Switcher) Serve(l net.Listener) {
	log.Printf("switcher running, addr is %v\n", l.Addr())
	for {
		conn, err := l.Accept()
		if err != nil {
			break
		}
		go switcher.ServeConn(conn)
	}
	log.Println("switcher stopped")
}

func (switcher *Switcher) ServeConn(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()

	if switcher.password != "" {
		cc, err := cipherconn.New(conn, switcher.password)
		if err != nil {
			log.Printf("host(remote=%v) upgrade failed: %v\n", remote, err)
			return
		}
		conn = cc
	}

	ctx, err := switcher.UpgradeToContext(NewTcpPacketConn(conn))
	if err != nil {
		log.Printf("host(remote=%v) upgrade failed: %v\n", remote, err)
		return
	}
	switcher.ServeContext(ctx, remote)
}

func (switcher *Switcher) ServeContext(ctx *switchContext, remote string) {

	switcher.attach(ctx)

	log.Printf("host(domain=%v ip=%v remote=%v active=%v) joined\n",
		ctx.domain, ctx.ip, remote, atomic.AddInt32(&switcher.activeCtxsLen, 1))

	err := switcher.contextReadLoop(ctx)

	log.Printf("host(domain=%v ip=%v remote=%v active=%v) exit. err=%v\n",
		ctx.domain, ctx.ip, remote, atomic.AddInt32(&switcher.activeCtxsLen, -1), err)

	switcher.detach(ctx)
}

//
// contextReadLoop
// 不断读取连接中的数据，并根据DistIP对数据进行分发
//
func (switcher *Switcher) contextReadLoop(ctx *switchContext) error {
	for {
		pb := NewPacketBufs()

		err := ctx.host.pc.ReadPacket(pb)
		if err != nil {
			return err
		}

		if pb.head.Cmd() == CmdAlive {
			continue
		}

		// log.Printf("%v %v\n", pb.head.CmdStr(), pb.head)

		switch pb.head[0] {
		case CmdOpenStream:
			go switcher.switchOpen(ctx, pb)
		case CmdPushStreamData:
			//
			// 使用队列进行解耦，降低数据包对cmd的影响
			// 可以使用多个队列，降低相互影响
			// CPUID算法决定队列流量的公平性（公平性待检验）
			//
			switcher.dataChans[pb.head.CPUID()] <- pb
			// switcher.switchData(pb)
		default:
			go switcher.switchData(pb)
		}
	}
}

func (switcher *Switcher) switchOpen(caller *switchContext, pb *PacketBufs) {
	domain := string(pb.payload)
	if domain == "" {
		pb.SetPayload([]byte(caller.domain))
		switcher.switchData(pb)
		return
	}

	//
	// 通过域名解析进行请求路由
	//
	it, found := switcher.domainCtxs.Load(domain)
	if !found {
		log.Printf("resolve domain failed: '%v' not found", domain)
		return
	}
	dist := it.(*switchContext)

	pb.SetDistIP(dist.ip)
	pb.SetPayload([]byte(caller.domain))
	dist.host.pc.WritePacket(pb)
}

func (switcher *Switcher) pushDataLoop() {
	var wg sync.WaitGroup
	for i, ch := range switcher.dataChans {
		wg.Add(1)
		go func(index int, ch chan *PacketBufs) {
			for pb := range ch {
				switcher.switchData(pb)
			}
			wg.Done()
		}(i, ch)
	}
	wg.Wait()
}

func (switcher *Switcher) switchData(pb *PacketBufs) {
	// write pb to dist hosts
	it, found := switcher.ctxs.Load(pb.head.DistIP())
	if !found {
		log.Printf("%v%v -> %v discard\n", pb.head.CmdStr(), pb.head.Src(), pb.head.Dist())
		return
	}

	err := it.(*switchContext).host.pc.WritePacket(pb)
	if err != nil {
		log.Printf("%v%v -> %v write failed. %v\n",
			pb.head.CmdStr(), pb.head.Src(), pb.head.Dist(), err)
	}
}
