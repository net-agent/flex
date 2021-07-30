package switcher

import (
	"errors"
	"log"
	"net"
	"sync"

	"github.com/net-agent/flex/packet"
)

type Server struct {
	freeIps     chan uint16
	nodeDomains sync.Map // map[string]*Context
	nodeIps     sync.Map // map[uint16]*Context
}

func NewServer() *Server {
	return &Server{
		freeIps: getFreeIpCh(1, 0xFFFF),
	}
}

func (s *Server) Serve(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		go s.ServeConn(packet.NewWithConn(conn))
	}
}

func (s *Server) Close() error {
	return nil
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

// ResolveOpenCmd 解析open命令
func (s *Server) ResolveOpenCmd(caller *Context, pbuf *packet.Buffer) {
	distDomain := string(pbuf.Payload)
	if distDomain == "" {
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
		log.Printf("node not found ip=%v\n", distIP)
		return
	}
	ctx := it.(*Context)
	err := ctx.WriteBuffer(pbuf)
	if err != nil {
		// 此处记录写入失败的日志，但不退出循环
		log.Printf("node.write failed ip=%v\n", distIP)
		return
	}
}
