package switcher

import (
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/warning"
)

func TestAttachCtx(t *testing.T) {
	s := NewServer("", nil)
	ctx := NewContext(nil, "test1", "")

	// 测试分支：正确处理
	err := s.AttachCtx(ctx)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试分支：替换旧的ctx（还是会返回成功，但是会清理IP）
	err = s.AttachCtx(ctx)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 测试分支：domain不同但是ip相同
	ctx2 := NewContext(nil, "test2", "")
	err = s.AttachCtx(ctx2)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// 模拟设置一个未被清理的ip地址（ip的分配是自增的，所以使用ctx2.IP+1来模拟）
	s.nodeIps.Store(ctx2.IP+1, nil)
	ctx3 := NewContext(nil, "test3", "")
	err = s.AttachCtx(ctx3)
	if err != errContextIPExist {
		t.Errorf("unexpected err=%v\n", err)
		return
	}

	// print
	table := s.GetCtxRecords()
	for index, row := range table {
		log.Printf("%v: %v\n", index, row)
	}
	<-time.After(time.Millisecond * 100)
	s.DetachCtx(ctx2)
	s.GetCtxRecords()
}

func TestAttachErr_GetIP(t *testing.T) {
	s := NewServer("", nil)
	s.ipm, _ = numsrc.NewManager(0, 9, 10)
	var err error

	err = s.AttachCtx(NewContext(nil, "test1", ""))
	if err != nil {
		t.Error(err)
		return
	}
	err = s.AttachCtx(NewContext(nil, "test2", ""))
	if err != errGetFreeContextIPFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestServerRun(t *testing.T) {
	addr := "localhost:39603"
	pswd := "testpswd"
	errChan := make(chan *warning.Message, 10)
	s := NewServer(pswd, errChan)
	s.Close() // 提高代码覆盖度

	// test popupError
	var waitErrChanClose sync.WaitGroup
	waitErrChanClose.Add(1)
	go func() {
		for err := range errChan {
			log.Printf("popupError: %v\n", err)
		}
		waitErrChanClose.Done()
	}()

	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Errorf("listen failed. err=%v\n", err)
		return
	}
	go s.Run(l)
	go s.Run(l) // 提高代码覆盖度

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed. err=%v\n", err)
		return
	}
	pc := packet.NewWithConn(c)
	handshake.UpgradeRequest(pc, "test", "", pswd)

	s.PopupWarning("test popupwarning", "reason")
	s.Close()
	close(errChan)
	waitErrChanClose.Wait()
}
