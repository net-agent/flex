package switcher

import (
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

func TestRouterPingDomain(t *testing.T) {
	_, node1, _ := initTestEnv("test1", "test2")

	var err error

	// 测试用例：a ping server
	_, err = node1.PingDomain("", time.Second)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试用例：a ping b
	_, err = node1.PingDomain("test2", time.Second)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试用例：发送一个pbuf到未知的ip，触发route pbuf failed
	pbuf := packet.NewBuffer()
	pbuf.SetDist(100, 100)
	err = node1.WriteBuffer(pbuf)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试用例：ping一个不存在的domain
	_, err = node1.PingDomain("notexistdomain", time.Second)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
}

func TestRouterForwardNilConn(t *testing.T) {
	s := NewServer("", nil, nil)
	ctx := NewContext(1, nil, "test", "", nil)
	ctx.IP = 2
	s.registry.ipMu.Lock()
	s.registry.ipIndex[ctx.IP] = ctx
	s.registry.ipMu.Unlock()

	// 错误用例：因为ctx的pc是空，所以会触发dist.writeBuffer的错误
	pbuf := packet.NewBuffer()
	pbuf.SetDist(2, 100)
	s.router.forward(pbuf)
}

func TestRouterForwardClosedContext(t *testing.T) {
	s := NewServer("", nil, nil)
	ctx := NewContext(1, nil, "test", "", nil)
	ctx.IP = 2
	s.registry.ipMu.Lock()
	s.registry.ipIndex[ctx.IP] = ctx
	s.registry.ipMu.Unlock()

	// 关闭context，使enqueueForward返回错误
	ctx.release()

	pbuf := packet.NewBuffer()
	pbuf.SetDist(2, 100)
	s.router.forward(pbuf)
}

func TestRouterOpenStream(t *testing.T) {
	_, node1, _ := initTestEnv("test1", "test2")

	// 错误用例：直接dial一个未开放的端口，会返回错误
	_, err := node1.Dial("test2:80")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Printf("dial err=%v\n", err)

	// 错误用例：dial不存在的domain
	_, err = node1.Dial("notexistdomain:80")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Printf("dial err=%v\n", err)
}

func TestRouterAckPingDomain(t *testing.T) {
	s := NewServer("", nil, nil)
	caller := NewContext(1, nil, "test", "", nil)
	pbuf := packet.NewBuffer()

	// 分支覆盖：找不到port
	s.router.handleAckPingDomain(caller, pbuf)

	// 分支覆盖：deliverPingResponse returns false for non-channel value
	caller.pingBack.Store(uint16(0), 100)
	s.router.handleAckPingDomain(caller, pbuf)
}
