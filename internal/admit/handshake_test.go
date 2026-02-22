package admit

import (
	"errors"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

func TestHandshake(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		req, err := Accept(pc2, pswd)
		if err != nil {
			pc2.Close()
			return
		}
		resp := NewOKResponse(uint16(req.Version % 100))
		resp.WriteTo(pc2)
	}()

	_, err := Handshake(pc1, "test", "", pswd)
	if err != nil {
		t.Errorf("unexpected err=%v\n", err)
	}
}

func TestHandshake_WriteError(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()
	pc2.Close()

	_, err := Handshake(pc1, "test", "", pswd)
	if err == nil {
		t.Error("expected error")
	}
}

func TestHandshake_ReadError(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		pc2.ReadBuffer()
		pc2.Close()
	}()

	_, err := Handshake(pc1, "test", "", pswd)
	if err == nil {
		t.Error("expected error")
	}
}

func TestHandshake_UnmarshalError(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		pc2.ReadBuffer()
		pbuf := packet.NewBuffer()
		pbuf.SetPayload([]byte("a/2"))
		pc2.WriteBuffer(pbuf)
	}()

	_, err := Handshake(pc1, "test", "", pswd)
	if err == nil {
		t.Error("expected error")
	}
}

func TestHandshake_ServerError(t *testing.T) {
	pswd := "testpswd"
	badPswd := pswd + "_bad"
	pc1, pc2 := packet.Pipe()

	go func() {
		_, err := Accept(pc2, pswd)
		if err != nil {
			resp := NewErrResponse(-1, "handshake rejected")
			resp.WriteTo(pc2)
			return
		}
	}()

	_, err := Handshake(pc1, "test", "", badPswd)
	if err == nil {
		t.Error("expected error")
	}
}

func TestHandshake_VersionMismatch(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		pc2.ReadBuffer()
		var resp Response
		resp.ErrCode = 0
		resp.Version = packet.VERSION + 1
		resp.WriteTo(pc2)
	}()

	_, err := Handshake(pc1, "test", "", pswd)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("unexpected err=%v\n", err)
	}
}

func TestAccept_ReadError(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()
	pc1.Close()

	_, err := Accept(pc2, pswd)
	if err == nil {
		t.Error("expected error")
	}
}

func TestAccept_UnmarshalError(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		pbuf := packet.NewBuffer()
		pbuf.SetPayload([]byte("a/2"))
		pc1.WriteBuffer(pbuf)
	}()

	_, err := Accept(pc2, pswd)
	if err == nil {
		t.Error("expected error")
	}
}

func TestAccept_VersionMismatch(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		var req Request
		req.Version = packet.VERSION + 1
		req.Timestamp = time.Now().UnixNano()
		req.Sum = req.CalcSum(pswd)
		req.WriteTo(pc1)
	}()

	_, err := Accept(pc2, pswd)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("unexpected err=%v\n", err)
	}
}

func TestAccept_InvalidPassword(t *testing.T) {
	pswd := "testpswd"
	badPswd := pswd + "_bad"
	pc1, pc2 := packet.Pipe()

	go func() {
		var req Request
		req.Version = packet.VERSION
		req.Timestamp = time.Now().UnixNano()
		req.Sum = req.CalcSum(badPswd)
		req.WriteTo(pc1)
	}()

	_, err := Accept(pc2, pswd)
	if !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("unexpected err=%v\n", err)
	}
}

func TestAccept_InvalidDomain(t *testing.T) {
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
			req.Timestamp = time.Now().UnixNano()
			req.Sum = req.CalcSum(pswd)
			req.WriteTo(pc1)
		}()

		_, err := Accept(pc2, pswd)
		if !errors.Is(err, ErrInvalidDomain) {
			t.Errorf("unexpected err=%v\n", err)
			return false
		}
		return true
	}

	for _, domain := range badDomainCases {
		if !testWithDomain(domain) {
			t.Errorf("test failed with domain='%v'\n", domain)
			return
		}
	}
}

func TestAccept_TimestampExpired(t *testing.T) {
	pswd := "testpswd"
	pc1, pc2 := packet.Pipe()

	go func() {
		var req Request
		req.Version = packet.VERSION
		req.Domain = "test"
		req.Timestamp = time.Now().Add(-10 * time.Minute).UnixNano()
		req.Sum = req.CalcSum(pswd)
		req.WriteTo(pc1)
	}()

	_, err := Accept(pc2, pswd)
	if !errors.Is(err, ErrTimestampExpired) {
		t.Errorf("unexpected err=%v, want ErrTimestampExpired\n", err)
	}
}
