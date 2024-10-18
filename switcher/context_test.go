package switcher

import (
	"log"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/packet"
)

func TestContextPing(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")

	ctx1, err := s.GetContextByDomain("test1")
	if err != nil {
		t.Error(err)
		return
	}
	_, err = ctx1.Ping(time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	// 修改node2的domain，让客户端被ping时返回错误
	node2.SetDomain("xxxx")
	ctx2, err := s.GetContextByDomain("test2")
	if err != nil {
		t.Error(err)
		return
	}
	_, err = ctx2.Ping(time.Second)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Println(err)

	// 错误分支：WriteBuffer failed
	node1.Conn.Close()
	_, err = ctx1.Ping(time.Second)
	if err != errPingWriteFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestPingErr_Timeout(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd)
	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.HandlePacketConn(pc2, nil, nil)
	go s.HandlePacketConn(pc4, nil, nil)

	var waitUpgradeReady sync.WaitGroup

	waitUpgradeReady.Add(1)
	go func() {
		handshake.UpgradeRequest(pc1, "test1", "", pswd)
		waitUpgradeReady.Done()

		// 只读取buffer，不回复buffer。这样就会出现pingTimeout
		for {
			pc1.ReadBuffer()
		}
	}()

	waitUpgradeReady.Add(1)
	go func() {
		ip, _ := handshake.UpgradeRequest(pc3, "test2", "", pswd)
		waitUpgradeReady.Done()
		n := node.New(pc3)
		n.SetIP(ip)
		n.SetDomain("test2")
		n.Run()
	}()

	waitUpgradeReady.Wait()

	ctx1, err := s.GetContextByDomain("test1")
	if err != nil {
		t.Error(err)
		return
	}
	_, err = ctx1.Ping(time.Millisecond * 100)
	if err != errPingTimeout {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}
