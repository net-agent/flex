package switcher

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/net-agent/flex/packet"
)

type Server struct {
	nodeDomains sync.Map // map[string]*Context
	nodeIps     sync.Map // map[uint16]*Context

	dataChans     []chan *packet.Buffer
	dataChansMask int
	dataOpenCount uint32
}

func NewServer() *Server {
	//
	// 创建与CPU核心数相关的goroutine数量
	// 为了保证运行时效率，数量为2^n
	num := runtime.NumCPU()
	for {
		next := num ^ (num - 1)
		if next == 0 {
			break
		}
		num = next
	}
	num = num << 1
	mask := num - 1

	var chans []chan *packet.Buffer
	for i := 0; i < num; i++ {
		chans = append(chans, make(chan *packet.Buffer, 2048))
	}

	return &Server{
		dataChans:     chans,
		dataChansMask: mask,
	}
}

// Run 采用多线程的并发发送数据
func (s *Server) Run() {
	var wait sync.WaitGroup
	for i, ch := range s.dataChans {
		wait.Add(1)
		go func(index int, ch chan *packet.Buffer) {
			for pbuf := range ch {
				s.RouteBuffer(pbuf)
			}
			wait.Done()
		}(i, ch)
	}
	wait.Wait()
}

// AttachCtx 附加上下文
func (s *Server) AttachCtx(ctx *Context) error {
	_, loaded := s.nodeDomains.LoadOrStore(ctx.Domain, ctx)
	if loaded {
		return errors.New("domain exist")
	}

	_, loaded = s.nodeIps.LoadOrStore(ctx.IP, ctx)
	if loaded {
		s.nodeDomains.Delete(ctx.Domain)
		return errors.New("ip exist")
	}

	return nil
}

// DetachCtx 分离上下文
func (s *Server) DetachCtx(ctx *Context) {
	s.nodeDomains.Delete(ctx.Domain)
	s.nodeIps.Delete(ctx.IP)
}

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) error {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}

	ctx := NewContext("default", "localhoste", 0, pc)

	err = s.AttachCtx(ctx)
	if err != nil {
		return err
	}
	defer s.DetachCtx(ctx)

	// response
	err = pc.WriteBuffer(pbuf)
	if err != nil {
		return err
	}

	// read loop
	for {
		pbuf, err = pc.ReadBuffer()
		if err != nil {
			return err
		}

		if pbuf.Cmd() == packet.CmdAlive {
			continue
		}

		switch pbuf.Cmd() {
		case packet.CmdOpenStream:
			go s.ResolveOpenCmd(ctx, pbuf)
		case packet.CmdPushStreamData:
			s.RouteDataBuffer(pbuf)
		default:
			go s.RouteBuffer(pbuf)
		}

	}
}

// ResolveOpenCmd 解析open命令
func (s *Server) ResolveOpenCmd(caller *Context, pbuf *packet.Buffer) {
	distDomain := string(pbuf.Payload)
	if distDomain == "" {
		openCount := atomic.AddUint32(&s.dataOpenCount, 1)
		pbuf.SetToken(byte(openCount))
		pbuf.SetPayload([]byte(caller.Domain))
		s.RouteBuffer(pbuf)
		return
	}

	//
	// 进行域名解析，确定目标节点
	//
	it, found := s.nodeDomains.Load(distDomain)
	if !found {
		return
	}
	distCtx := it.(*Context)

	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload([]byte(caller.Domain))
	distCtx.Conn.WriteBuffer(pbuf)
}

// RouteBuffer 根据DistIP路由数据包，这些包可以无序
func (s *Server) RouteBuffer(pbuf *packet.Buffer) {
	distIP := pbuf.DistIP()
	it, found := s.nodeIps.Load(distIP)
	if !found {
		// 此处记录找不到ctx的错误，但不退出循环
		return
	}
	ctx := it.(*Context)
	err := ctx.WriteBuffer(pbuf)
	if err != nil {
		// 此处记录写入失败的日志，但不退出循环
		return
	}
}

// RouteData 根据DistIP路由数据包，这些包需要保持有序
func (s *Server) RouteDataBuffer(pbuf *packet.Buffer) {
	s.dataChans[pbuf.Token()&byte(s.dataChansMask)] <- pbuf
}
