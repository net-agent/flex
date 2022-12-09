package switcher

import (
	"fmt"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

func initTestEnv(domain1, domain2 string) (*Server, *node.Node, *node.Node) {
	pswd := "testpswd"
	s := NewServer(pswd)
	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.HandlePacketConn(pc2)
	go s.HandlePacketConn(pc4)

	node1, _ := UpgradeRequest(pc1, domain1, "", pswd)
	node2, _ := UpgradeRequest(pc3, domain2, "", pswd)
	go node1.Run()
	go node2.Run()

	return s, node1, node2
}

func TestGetContextByDomain(t *testing.T) {
	var err error
	s := NewServer("")

	// 错误分支：domain不正确
	_, err = s.GetContextByDomain("")
	if err != errInvalidDomain {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 错误分支：domain不存在
	_, err = s.GetContextByDomain("testdomain1")
	if err != errDomainNotFound {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 错误分支：context类型错误
	s.nodeDomains.Store("testdomain2", 100)
	_, err = s.GetContextByDomain("testdomain2")
	if err != errConvertContextFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 正确分支
	ctx := NewContext(nil, "testdomain3", "")
	s.nodeDomains.Store(ctx.Domain, ctx)
	returnCtx, err := s.GetContextByDomain(ctx.Domain)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	if returnCtx != ctx {
		t.Error("unexpected return value")
		return
	}
}

func TestGetContextByIP(t *testing.T) {
	var err error
	s := NewServer("")

	// 错误分支：invalid context ip
	_, err = s.GetContextByIP(vars.LocalIP)
	if err != errInvalidContextIP {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	_, err = s.GetContextByIP(vars.SwitcherIP)
	if err != errInvalidContextIP {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 错误分支：ip not found
	_, err = s.GetContextByIP(100)
	if err != errContextIPNotFound {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 错误分支：context类型错误
	// 备注：不能直接s.nodeIps.Store(100, 3000)，这里的100会被当做int对待，int(100) != uint16(100)
	s.nodeIps.Store(uint16(100), 3000)
	_, err = s.GetContextByIP(uint16(100))
	if err != errConvertContextFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 正确分支
	ctx := NewContext(nil, "testdomain", "")
	ctx.IP = 200
	s.nodeIps.Store(ctx.IP, ctx)
	returnCtx, err := s.GetContextByIP(ctx.IP)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
	if returnCtx != ctx {
		t.Error("unexpected return value")
		return
	}
}

func TestHandleDefaultPbufWithPing(t *testing.T) {
	_, node1, _ := initTestEnv("test1", "test2")

	// 测试用例：a ping b
	_, err := node1.PingDomain("test2", time.Second)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试用例：a ping server
	_, err = node1.PingDomain("", time.Second)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试用例：发送一个pbuf到未知的ip，触发route pbuf failed
	pbuf := packet.NewBuffer(nil)
	pbuf.SetDist(100, 100)
	err = node1.WriteBuffer(pbuf)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试用例：ping一个不存在的domain
	_, err = node1.PingDomain("notexistdomain", time.Second)
	if err == nil {
		t.Error("unexpected ni err")
		return
	}
}

func TestHandleCmdOpenStream(t *testing.T) {
	_, node1, _ := initTestEnv("test1", "test2")

	// 错误用例：直接dial一个未开放的端口，会返回错误
	_, err := node1.Dial("test2:80")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	fmt.Printf("dial err=%v\n", err)

	// 错误用例：
	_, err = node1.Dial("notexistdomain:80")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	fmt.Printf("dial err=%v\n", err)
}
