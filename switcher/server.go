package switcher

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

type Server struct {
	password      string
	freeIps       chan uint16
	nodeDomains   sync.Map   // map[string]*Context
	nodeIps       sync.Map   // map[uint16]*Context
	ctxRecords    []*Context // 不断自增（todo：fix memory leak）
	ctxRecordsMut sync.Mutex
}

func NewServer(password string) *Server {
	return &Server{
		password: password,
		freeIps:  getFreeIpCh(1, vars.MaxIP),
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
	oldCtx, replaced := s.replaceDomain(ctx)
	if !replaced {
		ctx.Conn.Close()
		return errors.New("replace domain context failed")
	}

	if oldCtx != nil {
		oldCtx.attached = false
		oldCtx.Conn.Close()
		s.nodeIps.Delete(oldCtx.IP)
	}

	_, loaded := s.nodeIps.LoadOrStore(ctx.IP, ctx)
	if !loaded {
		return nil
	}

	ctx.attached = false
	ctx.Conn.Close()
	s.nodeDomains.Delete(ctx.Domain)

	return errors.New("ip exist")
}

// replaceDomain 如果存在同名的context，进行检测
func (s *Server) replaceDomain(newCtx *Context) (oldCtx *Context, replaced bool) {
	it, loaded := s.nodeDomains.LoadOrStore(newCtx.Domain, newCtx)
	if !loaded {
		s.pushCtxRecord(newCtx)
		newCtx.attached = true
		return nil, true
	}
	oldCtx, ok := it.(*Context)
	if !ok {
		s.nodeDomains.Store(newCtx.Domain, newCtx)
		s.pushCtxRecord(newCtx)
		newCtx.attached = true
		return nil, true
	}

	// 增加会话探活机制，要求domain当前绑定的ctx在3秒内进行回应
	// 如果3秒内未进行回应可以判定是“僵尸”连接
	// 3秒内收到回应，则拒绝新连接绑定
	_, err := oldCtx.Ping(time.Second * 3)
	if err != nil {
		s.nodeDomains.Store(newCtx.Domain, newCtx)
		s.pushCtxRecord(newCtx)
		newCtx.attached = true
		return oldCtx, true
	}

	return oldCtx, false
}

func (s *Server) pushCtxRecord(ctx *Context) {
	s.ctxRecordsMut.Lock()
	defer s.ctxRecordsMut.Unlock()
	s.ctxRecords = append(s.ctxRecords, ctx)
}

func (s *Server) PrintCtxRecords() [][]interface{} {
	now := time.Now()
	titles := []interface{}{
		"id", "domain", "mac", "vip", "conn",
		"state", "workTime",
		"attachTime", "detachTime"}

	ret := [][]interface{}{titles}

	s.ctxRecordsMut.Lock()
	defer s.ctxRecordsMut.Unlock()

	for _, ctx := range s.ctxRecords {
		state := "working"
		workTime := now.Sub(ctx.AttachTime)

		if ctx.DetachTime.After(ctx.AttachTime) {
			state = "done"
			workTime = ctx.DetachTime.Sub(ctx.AttachTime)
		}
		vals := []interface{}{
			ctx.id, ctx.Domain, ctx.Mac, ctx.IP, "",
			state, fmt.Sprint(workTime),
			ctx.AttachTime, ctx.DetachTime,
		}
		ret = append(ret, vals)
	}

	return ret
}

// DetachCtx 分离上下文
func (s *Server) DetachCtx(ctx *Context) {
	if ctx.attached {
		s.nodeDomains.Delete(ctx.Domain)
		s.nodeIps.Delete(ctx.IP)
		ctx.Conn = nil
		ctx.attached = false
		ctx.DetachTime = time.Now()
	}
}

// ResolveOpenCmd 解析open命令
func (s *Server) ResolveOpenCmd(caller *Context, pbuf *packet.Buffer) {
	distDomain := strings.ToLower(string(pbuf.Payload))
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
		errInfo := fmt.Sprintf("resolve failed, domain='%v'", distDomain)
		log.Println(errInfo)
		pbuf.SetOpenACK(errInfo)
		pbuf.SetSrcIP(0)
		caller.WriteBuffer(pbuf)
		return
	}
	distCtx := it.(*Context)

	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload([]byte(caller.Domain))
	distCtx.WriteBuffer(pbuf)
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
