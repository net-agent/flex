package switcher

import (
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/packet"
)

func TestServerRun(t *testing.T) {
	addr := "localhost:39603"
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	s.Close() // 提高代码覆盖度

	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Errorf("listen failed. err=%v\n", err)
		return
	}
	go s.Serve(l)
	go s.Serve(l) // 提高代码覆盖度

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed. err=%v\n", err)
		return
	}
	pc := packet.NewWithConn(c)
	handshake.UpgradeRequest(pc, "test", "", pswd)

	s.Close()
}

func TestServeConn(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()

	// 模拟客户端请求
	go func() {
		handshake.UpgradeRequest(pc1, "test", "", pswd)
		pc1.Close()
	}()

	s.ServeConn(pc2)
}

// 模拟domain重复的场景
func TestHandlePCErr_DomainExist(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	s.OnContextStart = func(ctx *Context) {
		log.Printf("pbuf loop start, domain='%v' ip='%v'", ctx.Domain, ctx.IP)
	}
	s.OnContextStop = func(ctx *Context, duration time.Duration) {
		log.Printf("pbuf loop stopped, dur=%v\n", duration)
	}

	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.ServeConn(pc2)
	go s.ServeConn(pc4)

	// 先正常接入一个domain=test的连接
	ip, err := handshake.UpgradeRequest(pc1, "test", "", pswd)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	node := node.New(pc1)
	node.SetIP(ip)
	node.SetDomain("test")
	var waitNodeRun sync.WaitGroup
	waitNodeRun.Add(1)
	go func() {
		node.Serve()
		waitNodeRun.Done()
	}()

	// 模拟重复接入一个domain=test的连接
	_, err = handshake.UpgradeRequest(pc3, "test", "", pswd)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Printf("expected err=%v\n", err)
	pc1.Close()
	waitNodeRun.Wait()
}

// 模拟domain重复的场景
func TestHandlePCErr_Password(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()

	go func() {
		// 第一步：发送UpgradeRequest
		// 第二步：发完后关闭连接
		var req handshake.Request
		req.Domain = "test"
		req.Version = packet.VERSION
		req.Sum = req.CalcSum(pswd + "_badpswd")
		pbuf := packet.NewBuffer(nil)
		pbuf.SetPayload(req.Marshal())
		pc1.WriteBuffer(pbuf)
		pc1.ReadBuffer()
	}()

	err := s.ServeConn(pc2)
	if err != handshake.ErrInvalidPassword {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	log.Printf("expected err=%v\n", err)
}

// 模拟服务端在应答UpgradeRequest之前连接断开的情况
func TestHandlePCErr_WriteResponse(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()

	go func() {
		// 第一步：发送UpgradeRequest
		// 第二步：发完后关闭连接
		var req handshake.Request
		req.Domain = "test"
		req.Version = packet.VERSION
		req.Sum = req.CalcSum(pswd)
		pbuf := packet.NewBuffer(nil)
		pbuf.SetPayload(req.Marshal())
		pc1.WriteBuffer(pbuf)
		pc1.Close()
	}()

	err := s.ServeConn(pc2)
	if err != errHandlePCWriteFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	log.Printf("expected err=%v\n", err)
}

func TestServerInfo(t *testing.T) {
	server := NewServer("test-pwd", nil, nil)

	stats := server.GetStats()
	if stats == nil {
		t.Error("expected stats to be not nil")
	}

	clients := server.GetClients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}

func TestGetClientsWithData(t *testing.T) {
	s, _, _ := initTestEnv("test1", "test2")

	clients := s.GetClients()
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(clients))
	}

	stats := s.GetStats()
	if stats.ActiveConnections != 2 {
		t.Errorf("expected 2 active connections, got %d", stats.ActiveConnections)
	}
	if stats.TotalContexts < 2 {
		t.Errorf("expected TotalContexts >= 2, got %d", stats.TotalContexts)
	}

	// 验证 client 信息完整性
	for _, c := range clients {
		if c.Domain == "" {
			t.Error("expected non-empty domain")
		}
		if c.IP == 0 {
			t.Error("expected non-zero IP")
		}
	}
}
