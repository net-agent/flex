package switcher

import (
	"log"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
)

func TestContextPing(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")

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
	// 关闭底层连接后，ping 期望返回错误（通常为 timeout）
	node1.Conn.Close()
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

	var waitUpgradeReady sync.WaitGroup

	waitUpgradeReady.Add(1)
	go func() {
		admit.Handshake(pc1, "test1", "", pswd)
		waitUpgradeReady.Done()

		// 只读取buffer，不回复buffer。这样就会出现pingTimeout
		for {
			pc1.ReadBuffer()
		}
	}()

	waitUpgradeReady.Add(1)
	go func() {
		ip, _ := admit.Handshake(pc3, "test2", "", pswd)
		waitUpgradeReady.Done()
		n := node.New(pc3)
		n.SetIP(ip)
		n.SetDomain("test2")
		n.Serve()
	}()

	waitUpgradeReady.Wait()

	ctx1, err := s.registry.lookupByDomain("test1")
	if err != nil {
		t.Error(err)
		return
	}
	_, err = ctx1.ping(time.Millisecond * 100)
	if err != errPingTimeout {
		t.Errorf("unexpected err=%v\n", err)
		return
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
	pbuf := packet.NewBuffer()
	pbuf.SetCmd(packet.CmdOpenStream)
	ctx.recordIncoming(pbuf)
	if ctx.Stats.StreamCount != 1 {
		t.Errorf("expected StreamCount=1, got %v", ctx.Stats.StreamCount)
	}

	// CmdCloseStream 减少 StreamCount
	pbuf2 := packet.NewBuffer()
	pbuf2.SetCmd(packet.CmdCloseStream)
	ctx.recordIncoming(pbuf2)
	if ctx.Stats.StreamCount != 0 {
		t.Errorf("expected StreamCount=0, got %v", ctx.Stats.StreamCount)
	}

	// 其他命令不影响 StreamCount
	pbuf3 := packet.NewBuffer()
	pbuf3.SetCmd(packet.CmdPingDomain)
	ctx.recordIncoming(pbuf3)
	if ctx.Stats.StreamCount != 0 {
		t.Errorf("expected StreamCount=0, got %v", ctx.Stats.StreamCount)
	}

	// BytesReceived 应该累加
	if ctx.Stats.BytesReceived == 0 {
		t.Error("expected BytesReceived > 0")
	}
}

func TestEnqueueForward_FastPathClosed(t *testing.T) {
	ctx := &Context{
		forwardCh:   make(chan *packet.Buffer, 1),
		forwardDone: make(chan struct{}),
	}
	// 填满 channel，使 send case 不可用，只有 forwardDone 可选
	ctx.forwardCh <- packet.NewBuffer()
	close(ctx.forwardDone)

	err := ctx.enqueueForward(packet.NewBuffer())
	if err == nil {
		t.Error("expected error for closed context")
	}
}

func TestEnqueueForward_SlowPathSuccess(t *testing.T) {
	ctx := &Context{
		forwardCh:   make(chan *packet.Buffer, 1),
		forwardDone: make(chan struct{}),
	}
	// 填满 channel，使快路径失败
	ctx.forwardCh <- packet.NewBuffer()

	// 延迟腾出空间
	go func() {
		time.Sleep(50 * time.Millisecond)
		<-ctx.forwardCh
	}()

	err := ctx.enqueueForward(packet.NewBuffer())
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestEnqueueForward_SlowPathClosed(t *testing.T) {
	ctx := &Context{
		forwardCh:   make(chan *packet.Buffer, 1),
		forwardDone: make(chan struct{}),
	}
	// 填满 channel，使快路径失败
	ctx.forwardCh <- packet.NewBuffer()

	// 延迟关闭 context
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(ctx.forwardDone)
	}()

	err := ctx.enqueueForward(packet.NewBuffer())
	if err == nil {
		t.Error("expected error for closed context")
	}
}

func TestEnqueueForward_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow enqueue timeout test")
	}

	ctx := &Context{
		forwardCh:   make(chan *packet.Buffer, 1),
		forwardDone: make(chan struct{}),
	}
	// 填满 channel，不消费，不关闭 — 触发 5s 超时
	ctx.forwardCh <- packet.NewBuffer()

	start := time.Now()
	err := ctx.enqueueForward(packet.NewBuffer())
	dur := time.Since(start)

	if err == nil {
		t.Error("expected timeout error")
	}
	if dur < 4*time.Second {
		t.Errorf("expected ~5s timeout, got %v", dur)
	}
}

func TestRunForwardLoopChannelClose(t *testing.T) {
	ctx := &Context{
		forwardCh:   make(chan *packet.Buffer, 1),
		forwardDone: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		ctx.runForwardLoop()
		close(done)
	}()

	// 关闭 channel 触发 !ok 分支
	close(ctx.forwardCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("runForwardLoop did not exit after channel close")
	}
}

func TestGetID(t *testing.T) {
	ctx := NewContext(42, nil, "test", "", nil)
	if ctx.GetID() != 42 {
		t.Errorf("expected 42, got %v", ctx.GetID())
	}
}
