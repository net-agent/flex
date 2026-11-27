package switcher

import (
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
)

func TestRouterPingDomain(t *testing.T) {
	_, node1, _ := initTestEnv(t, "test1", "test2")

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

	// 测试用例：发送一个数据帧到未知虚拟 IP，触发 route failed。
	// 注意：不能使用零值 packet.NewBuffer()，其 Cmd=0 即 CmdAdmit（握手类），
	// 会被对端安全层当作非法握手帧并关闭链路。
	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	pbuf.SetHeader(packet.CmdPushStreamData, 100, 100, node1.GetIP(), 1000)
	if err := node1.WriteBuffer(pbuf); err != nil {
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

	// 错误用例：目标 ctx 没有 link，SendFrame 会失败并释放 frame。
	f, err := wire.NewData(wire.Addr{}, wire.Addr{IP: 2, Port: 100}, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	s.router.forward(f)
}

func TestRouterForwardClosedContext(t *testing.T) {
	s := NewServer("", nil, nil)
	ctx := NewContext(1, nil, "test", "", nil)
	ctx.IP = 2
	s.registry.ipMu.Lock()
	s.registry.ipIndex[ctx.IP] = ctx
	s.registry.ipMu.Unlock()

	// 关闭 context，使 SendFrame 返回错误。
	ctx.release()

	f, err := wire.NewData(wire.Addr{}, wire.Addr{IP: 2, Port: 100}, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	s.router.forward(f)
}

func TestRouterOpenStream(t *testing.T) {
	_, node1, _ := initTestEnv(t, "test1", "test2")

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

	f1, err := wire.NewControl(wire.AckPingDomain, wire.Addr{IP: wire.MaxIP}, wire.Addr{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 分支覆盖：找不到 port
	s.router.handleAckPingDomain(caller, f1)

	// 分支覆盖：deliverPingResponse returns false for non-channel value
	caller.pingBack.Store(uint16(0), 100)
	f2, err := wire.NewControl(wire.AckPingDomain, wire.Addr{IP: wire.MaxIP}, wire.Addr{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.router.handleAckPingDomain(caller, f2)
}
