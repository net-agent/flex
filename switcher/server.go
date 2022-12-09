package switcher

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	errReplaceDomainFailed    = errors.New("replace domain context failed")
	errContextIPExist         = errors.New("context ip exist")
	errGetFreeContextIPFailed = errors.New("get unused ip failed")
)

type Server struct {
	listener      net.Listener
	password      string
	ipm           *numsrc.Manager
	nodeDomains   sync.Map   // map[string]*Context
	nodeIps       sync.Map   // map[uint16]*Context
	ctxRecords    []*Context // 不断自增（todo：fix memory leak）
	ctxRecordsMut sync.Mutex
}

func NewServer(password string) *Server {
	ipm, _ := numsrc.NewManager(1, 1, vars.MaxIP-1)

	return &Server{
		password: password,
		ipm:      ipm,
	}
}

func (s *Server) Run(l net.Listener) {
	if s.listener != nil {
		return
	}
	s.listener = l

	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		go s.HandlePacketConn(packet.NewWithConn(conn))
	}
}

func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// AttachCtx 附加上下文
func (s *Server) AttachCtx(ctx *Context) error {
	old, replaced := s.replaceDomain(ctx)
	if !replaced {
		// 当前存在同名的context，并且通过了ping测试
		return errReplaceDomainFailed
	}

	if old != nil {
		s.clearCtx(old)
	}

	ip, err := s.ipm.GetFreeNumberSrc()
	if err != nil {
		return errGetFreeContextIPFailed
	}
	ctx.IP = ip

	_, loaded := s.nodeIps.LoadOrStore(ctx.IP, ctx)
	if loaded {
		// 该IP已经存在绑定，并且还未清理
		s.nodeDomains.Delete(ctx.Domain)
		s.clearCtx(ctx)
		return errContextIPExist
	}

	ctx.AttachTime = time.Now()
	return nil
}

// replaceDomain 如果存在同名的context，进行检测
func (s *Server) replaceDomain(newCtx *Context) (curr *Context, replaceOK bool) {
	it, loaded := s.nodeDomains.LoadOrStore(newCtx.Domain, newCtx)
	if !loaded {
		// 不存在重名情况（绝大多数正常情况）
		s.pushCtxRecord(newCtx)
		newCtx.attached = true
		return nil, true
	}

	curr, ok := it.(*Context)
	if ok {
		// 增加会话探活机制，要求domain当前绑定的ctx在3秒内进行回应
		// 如果3秒内未进行回应可以判定是“僵尸”连接
		// 3秒内收到回应，则拒绝新连接绑定
		_, err := curr.Ping(time.Second * 3)
		if err == nil {
			return curr, false
		}

		// 没有ping通，则移除当前的context，注册新的context
		s.DetachCtx(curr)
	}

	s.nodeDomains.Store(newCtx.Domain, newCtx)
	s.pushCtxRecord(newCtx)
	newCtx.attached = true
	return curr, true
}

func (s *Server) pushCtxRecord(ctx *Context) {
	s.ctxRecordsMut.Lock()
	defer s.ctxRecordsMut.Unlock()
	s.ctxRecords = append(s.ctxRecords, ctx)
}

func (s *Server) GetCtxRecords() [][]string {
	now := time.Now()
	titles := []string{
		"id", "domain", "mac", "vip",
		"state", "workTime",
		"attachTime", "detachTime"}

	ret := [][]string{titles}

	s.ctxRecordsMut.Lock()
	defer s.ctxRecordsMut.Unlock()

	for _, ctx := range s.ctxRecords {
		state := "working"
		workTime := now.Sub(ctx.AttachTime)

		if ctx.DetachTime.After(ctx.AttachTime) {
			state = "done"
			workTime = ctx.DetachTime.Sub(ctx.AttachTime)
		}
		vals := []string{
			fmt.Sprint(ctx.id),
			ctx.Domain,
			ctx.Mac,
			fmt.Sprint(ctx.IP),
			state,
			fmt.Sprint(workTime),
			fmt.Sprint(ctx.AttachTime),
			fmt.Sprint(ctx.DetachTime),
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
		s.clearCtx(ctx)
	}
}

func (s *Server) clearCtx(ctx *Context) {
	if ctx.Conn != nil {
		ctx.Conn.Close()
	}
	ctx.Conn = nil
	ctx.attached = false
	ctx.DetachTime = time.Now()
	s.ipm.ReleaseNumberSrc(ctx.IP)
}
