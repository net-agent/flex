package switcher

import (
	"errors"
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/node"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
)

func TestContextPing(t *testing.T) {
	s, node1, node2 := initTestEnv(t, "test1", "test2")

	ctx1, err := s.registry.lookupByDomain("test1")
	if err != nil {
		t.Error(err)
		return
	}
	_, err = ctx1.ping(time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	// 修改node2的domain，让客户端被ping时返回错误
	node2.SetDomain("xxxx")
	ctx2, err := s.registry.lookupByDomain("test2")
	if err != nil {
		t.Error(err)
		return
	}
	_, err = ctx2.ping(time.Second)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Println(err)

	// 错误分支：WriteBuffer failed
	// 关闭节点（含底层连接）后，ping 期望返回错误（通常为 timeout）
	node1.Close()
	_, err = ctx1.ping(time.Second)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
}

func TestPingErr_Timeout(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.ServeConn(pc2)
	go s.ServeConn(pc4)

	lnk1, _ := testDialLink(t, pc1, "test1", pswd)
	defer lnk1.Close()
	// 只读取帧，不应答 ping，制造 ping timeout。
	go func() {
		for {
			f, err := lnk1.ReadFrame()
			if err != nil {
				return
			}
			f.Release()
		}
	}()

	lnk2, ip := testDialLink(t, pc3, "test2", pswd)
	n := node.NewFromLink(lnk2)
	n.SetIP(ip)
	n.SetDomain("test2")
	go n.Serve()
	defer n.Close()

	ctx1, err := s.registry.lookupByDomain("test1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ctx1.ping(100 * time.Millisecond); err != errPingTimeout {
		t.Fatalf("unexpected err=%v", err)
	}
}

func TestReadBufferNilConn(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)
	_, err := ctx.readBuffer()
	if err != errNilContextConn {
		t.Errorf("expected errNilContextConn, got %v", err)
	}
}

func TestRecordIncoming(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)

	// CmdOpenStream 增加 StreamCount
	f1, err := wire.NewControl(wire.CmdOpenStream, wire.Addr{}, wire.Addr{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx.recordIncoming(f1)
	f1.Release()
	if ctx.Stats.StreamCount != 1 {
		t.Errorf("expected StreamCount=1, got %v", ctx.Stats.StreamCount)
	}

	// CmdCloseStream 减少 StreamCount
	f2, err := wire.NewEmpty(wire.CmdCloseStream, wire.Addr{}, wire.Addr{})
	if err != nil {
		t.Fatal(err)
	}
	ctx.recordIncoming(f2)
	f2.Release()
	if ctx.Stats.StreamCount != 0 {
		t.Errorf("expected StreamCount=0, got %v", ctx.Stats.StreamCount)
	}

	// 其他命令不影响 StreamCount
	f3, err := wire.NewControl(wire.CmdPingDomain, wire.Addr{}, wire.Addr{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx.recordIncoming(f3)
	f3.Release()
	if ctx.Stats.StreamCount != 0 {
		t.Errorf("expected StreamCount=0, got %v", ctx.Stats.StreamCount)
	}

	// BytesReceived 应该累加
	if ctx.Stats.BytesReceived == 0 {
		t.Error("expected BytesReceived > 0")
	}
}

func TestContextSendFrameNoLink(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)
	f, err := wire.NewControl(wire.CmdPingDomain, wire.Addr{}, wire.Addr{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctx.SendFrame(f); !errors.Is(err, errNilContextConn) {
		t.Fatalf("err=%v", err)
	}
}

func TestGetID(t *testing.T) {
	ctx := NewContext(42, nil, "test", "", nil)
	if ctx.GetID() != 42 {
		t.Errorf("expected 42, got %v", ctx.GetID())
	}
}
