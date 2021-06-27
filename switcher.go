package flex

import (
	"errors"
	"fmt"
	"log"
	"net"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/net-agent/cipherconn"
)

// Switcher packet交换器，根据ip、port进行路由和分发
type Switcher struct {
	// 分发由host传上来的数据包
	dataChans []chan *PacketBufs // 用于保证Push的顺序和性能，同时避免writePool死锁

	availableIP chan HostIP
	ipMacBinds  sync.Map // map[HostIP]string
	macIPBinds  sync.Map // map[mac string]HostIP

	//
	// context indexs
	//
	ctxIndex      uint32
	ctxids        sync.Map // 等待重连 map[uint64]*switchContext
	ctxs          sync.Map // map[ip HostIP]*switchContext
	domainCtxs    sync.Map // map[domain string]*switchContext
	activeCtxsLen int32
}

func NewSwitcher(staticIP map[string]HostIP) *Switcher {
	switcher := &Switcher{
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

func (switcher *Switcher) Run(addr string, password string) {
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatal(err)
		return
	}

	switcher.Serve(l, password)
}

func (switcher *Switcher) Serve(l net.Listener, password string) {
	log.Printf("switcher running, addr is %v\n", l.Addr())
	for {
		conn, err := l.Accept()
		if err != nil {
			break
		}
		go func(conn net.Conn) {
			if password != "" {
				cc, err := cipherconn.New(conn, password)
				if err != nil {
					conn.Close()
					return
				}
				conn = cc
			}
			pc := NewTcpPacketConn(conn)
			switcher.ServePacketConn(pc)
		}(conn)
	}
	log.Println("switcher stopped")
}

func (switcher *Switcher) ServePacketConn(pc *PacketConn) {
	remote := pc.Origin().(interface{ RemoteAddr() net.Addr }).RemoteAddr().String()

	ctx, err := switcher.UpgradeToContext(pc)
	switch err {
	case ErrReconnected:
		return
	case nil:
		switcher.ServeContext(ctx, remote)
		pc.Close()
	default:
		log.Printf("upgrade failed, err=%v\n", err)
		pc.Close()
		return
	}
}

func (switcher *Switcher) ServeContext(ctx *SwContext, remote string) {

	hostInfo := fmt.Sprintf("host(domain=%v ip=%v remote=%v)", ctx.domain, ctx.ip, remote)

	err := switcher.attach(ctx)
	if err != nil {
		log.Printf("%v attach failed, err=%v\n", hostInfo, err)
		return
	}
	log.Printf("%v joined, active=%v\n", hostInfo, atomic.AddInt32(&switcher.activeCtxsLen, 1))

	for {
		switcher.contextReadLoop(ctx)
		log.Printf("%v readloop stopped, waiting for recover.\n", hostInfo)

		// 尝试等待重连，并恢复当前会话
		if err := ctx.TryRecover(); err != nil {
			log.Printf("%v recover failed, err=%v\n", hostInfo, err)
			break
		}

		log.Printf("%v recovered\n", hostInfo)
	}

	log.Printf("%v exit, active=%v\n", hostInfo, atomic.AddInt32(&switcher.activeCtxsLen, -1))
	switcher.detach(ctx)
}

//
// contextReadLoop
// 不断读取连接中的数据，并根据DistIP对数据进行分发
//
func (switcher *Switcher) contextReadLoop(ctx *SwContext) error {
	defer ctx.host.pc.Close()

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

func (switcher *Switcher) switchOpen(caller *SwContext, pb *PacketBufs) {
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
	dist := it.(*SwContext)

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

	err := it.(*SwContext).host.pc.WritePacket(pb)
	if err != nil {
		log.Printf("%v%v -> %v write failed. %v\n",
			pb.head.CmdStr(), pb.head.Src(), pb.head.Dist(), err)
	}
}
