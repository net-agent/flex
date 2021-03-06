package packet

import (
	"bytes"
	"crypto/rand"
	"sync"
	"testing"
)

func TestRead(t *testing.T) {
	makeCase := func(cmd byte, payload []byte) *Buffer {
		pbuf := NewBuffer(nil)
		pbuf.SetCmd(cmd)
		pbuf.SetPayload(payload)
		return pbuf
	}

	runCase := func(index int, payload *Buffer) bool {
		c1, c2 := Pipe()
		var wg sync.WaitGroup

		writeOK := true
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c1.WriteBuffer(payload)
			if err != nil {
				t.Error(err, " index=", index)
				writeOK = false
				return
			}
			writeOK = true
		}()

		pbuf, err := c2.ReadBuffer()
		if err != nil {
			t.Error(err)
			return false
		}

		if !bytes.Equal(pbuf.Head[:], payload.Head[:]) {
			t.Error("read head not equal, index=", index)
			return false
		}
		if !bytes.Equal(pbuf.Payload, payload.Payload) {
			t.Error("read payload not equal, index=", index)
			return false
		}

		wg.Wait()
		return writeOK
	}

	bigBuf := make([]byte, 0xFFFF)
	rand.Read(bigBuf)

	cases := []*Buffer{
		makeCase(CmdPushStreamData, []byte("hello world")),
		makeCase(CmdPushStreamData, []byte("hello world")),
		makeCase(CmdPushStreamData, []byte("hello world")),
		makeCase(CmdPushStreamData, bigBuf),
	}

	for i, c := range cases {
		if !runCase(i, c) {
			return
		}
	}
}
