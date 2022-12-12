package switcher

import (
	"log"
	"testing"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/packet"
)

func TestHandlePacketConn(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd)
	pc1, pc2 := packet.Pipe()

	// 模拟客户端请求
	go func() {
		handshake.UpgradeRequest(pc1, "test", "", pswd)
		pc1.Close()
	}()

	s.HandlePacketConn(pc2)
}

// 模拟domain重复的场景
func TestHandlePCErr_DomainExist(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd)
	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.HandlePacketConn(pc2)
	go s.HandlePacketConn(pc4)

	ip, err := handshake.UpgradeRequest(pc1, "test", "", pswd)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	node := node.New(pc1)
	node.SetIP(ip)
	node.SetDomain("test")
	go node.Run()

	_, err = handshake.UpgradeRequest(pc3, "test", "", pswd)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Printf("expected err=%v\n", err)
}

// 模拟domain重复的场景
func TestHandlePCErr_Password(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd)
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

	err := s.HandlePacketConn(pc2)
	if err != handshake.ErrInvalidPassword {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	log.Printf("expected err=%v\n", err)
}

// 模拟服务端在应答UpgradeRequest之前连接断开的情况
func TestHandlePCErr_WriteResponse(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd)
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

	err := s.HandlePacketConn(pc2)
	if err != errHandlePCWriteFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	log.Printf("expected err=%v\n", err)
}
