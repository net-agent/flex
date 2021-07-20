package packet

import (
	"bytes"
	"testing"
)

type testCase struct {
	cmd              byte
	distIP, distPort uint16
	srcIP, srcPort   uint16
	token            byte
	payload          []byte
}

func TestSetGet(t *testing.T) {
	cases := []testCase{
		{1, 2, 3, 4, 5, 6, []byte("hello")},
	}

	for _, c := range cases {
		buf := NewBuffer()
		buf.SetHeader(c.cmd, c.distIP, c.distPort, c.srcIP, c.srcPort)
		buf.SetToken(c.token)
		buf.SetPayload(c.payload)

		if buf.Cmd() != c.cmd {
			t.Error("cmd not equal")
			return
		}
		if buf.DistIP() != c.distIP {
			t.Error("dist ip not equal")
			return
		}
		if buf.DistPort() != c.distPort {
			t.Error("dist port not equal")
			return
		}
		if buf.SrcIP() != c.srcIP {
			t.Error("src ip not equal")
			return
		}
		if buf.SrcPort() != c.srcPort {
			t.Error("src port not equal")
			return
		}
		if buf.Token() != c.token {
			t.Error("token not equal")
			return
		}
		if !bytes.Equal(buf.Payload, c.payload) {
			t.Error("payload not equal")
			return
		}
	}
}
