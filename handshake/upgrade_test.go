package handshake

import (
	"fmt"
	"testing"

	"github.com/net-agent/flex/v2/packet"
)

// TestUpgradeRequest 说明
// 构建客户端node到server端的模拟环境，通过在不同的阶段强制关闭连接来模拟异常行为
func TestUpgrade(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	// server process thread
	go func() {
		_, err := HandleUpgradeRequest(pc2, pswd)
		if err != nil {
			pc2.Close()
			return
		}
		var resp Response
		resp.ErrCode = 0
		resp.ErrMsg = ""
		resp.Version = packet.VERSION
		resp.WriteToPacketConn(pc2)
	}()

	_, err := UpgradeRequest(pc1, "test", "", pswd)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
	}
}

func TestUpgradeRequestErr_WriteBuffer(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	pc2.Close()

	_, err := UpgradeRequest(pc1, "test", "", pswd)
	if err != errUpgradeWriteFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestUpgradeRequestErr_ReadBuffer(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		// 读取一个packet后就关闭连接，导致客户端出现ReadBufferErr
		pc2.ReadBuffer()
		pc2.Close()
	}()

	_, err := UpgradeRequest(pc1, "test", "", pswd)
	if err != errUpgradeReadFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestUpgradeRequestErr_UnmarshalUpgradeResp(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		// 读取一个packet后就关闭连接，模拟发送一个无法Unmarshal的结构体
		pc2.ReadBuffer()

		pbuf := packet.NewBuffer(nil)
		pbuf.SetPayload([]byte("a/2"))
		pc2.WriteBuffer(pbuf)
	}()

	_, err := UpgradeRequest(pc1, "test", "", pswd)
	if err != errUnmarshalFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestUpgradeRequestErr_ServerSideErr(t *testing.T) {
	pswd := "testpswd"
	badPswd := pswd + "_bad"
	pc1, pc2 := packet.Pipe()

	go func() {
		_, err := HandleUpgradeRequest(pc2, pswd)
		if err != nil {
			// 构造错误的应答
			var resp Response
			resp.ErrCode = -1
			resp.ErrMsg = err.Error()
			resp.Version = packet.VERSION
			resp.WriteToPacketConn(pc2)
			return
		}

	}()

	_, err := UpgradeRequest(pc1, "test", "", badPswd)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	fmt.Printf("expected server side err=%v\n", err)
}

func TestUpgradeRequestErr_VersionNotMatch(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		// 读取第一个packet，然后构建错误的Response
		pc2.ReadBuffer()

		var resp Response
		resp.ErrCode = 0
		resp.ErrMsg = ""
		resp.Version = packet.VERSION + 1
		resp.WriteToPacketConn(pc2)
	}()

	_, err := UpgradeRequest(pc1, "test", "", pswd)
	if err != errPacketVersionNotMatch {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestHandleUpgradeErr_ReadErr(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	pc1.Close()

	_, err := HandleUpgradeRequest(pc2, pswd)
	if err != errUpgradeReadFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestHandleUpgradeErr_Unmarshal(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		pbuf := packet.NewBuffer(nil)
		pbuf.SetPayload([]byte("a/2"))
		pc1.WriteBuffer(pbuf)
	}()

	_, err := HandleUpgradeRequest(pc2, pswd)
	if err != errUnmarshalFailed {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestHandleUpgradeErr_Version(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		var req Request
		req.Version = packet.VERSION + 1
		req.Sum = req.CalcSum(pswd)

		pbuf := packet.NewBuffer(nil)
		pbuf.SetPayload(req.Marshal())
		pc1.WriteBuffer(pbuf)
	}()

	_, err := HandleUpgradeRequest(pc2, pswd)
	if err != errPacketVersionNotMatch {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestHandleUpgradeErr_Password(t *testing.T) {
	pswd := "testpswd"
	badPswd := pswd + "_bad"
	pc1, pc2 := packet.Pipe()

	go func() {
		var req Request
		req.Version = packet.VERSION
		req.Sum = req.CalcSum(badPswd)

		pbuf := packet.NewBuffer(nil)
		pbuf.SetPayload(req.Marshal())
		pc1.WriteBuffer(pbuf)
	}()

	_, err := HandleUpgradeRequest(pc2, pswd)
	if err != ErrInvalidPassword {
		t.Errorf("unexpected err=%v\n", err)
		return
	}
}

func TestHandleUpgradeErr_Domain(t *testing.T) {
	badDomainCases := []string{
		"", "local", "localhost", "local.des",
	}
	testWithDomain := func(domain string) bool {
		pswd := "testpswd"
		pc1, pc2 := packet.Pipe()

		go func() {
			var req Request
			req.Version = packet.VERSION
			req.Domain = domain
			req.Sum = req.CalcSum(pswd)

			pbuf := packet.NewBuffer(nil)
			pbuf.SetPayload(req.Marshal())
			pc1.WriteBuffer(pbuf)
		}()

		_, err := HandleUpgradeRequest(pc2, pswd)
		if err != ErrInvalidDomain {
			t.Errorf("unexpected err=%v\n", err)
			return false
		}
		return true
	}

	for _, domain := range badDomainCases {
		expected := testWithDomain(domain)
		if !expected {
			t.Errorf("test failed with domain='%v'\n", domain)
			return
		}
	}
}
