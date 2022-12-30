package packet

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRead(t *testing.T) {
	LOG_READ_BUFFER_HEADER = true
	LOG_WRITE_BUFFER_HEADER = true

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

func TestReadErr_SetDeadline(t *testing.T) {
	c1, c2 := net.Pipe()

	w := NewConnWriter(c2)
	r := NewConnReader(c1)
	go func() {
		w.WriteBuffer(NewBuffer(nil))
	}()

	c1.Close()
	r.SetReadTimeout(time.Second * 15)
	err := r.SetReadTimeout(time.Duration(0))
	assert.NotNil(t, err)
}

func TestReadErr_Header(t *testing.T) {
	c1, c2 := net.Pipe()

	w := NewConnWriter(c2)
	r := NewConnReader(c1)
	go func() {
		w.WriteBuffer(NewBuffer(nil))
		c2.Write([]byte{0x01})
	}()

	var err error

	// 设置超时时间，加快超时
	r.SetReadTimeout(time.Millisecond * 100)

	_, err = r.ReadBuffer()
	assert.Nil(t, err)

	_, err = r.ReadBuffer()
	assert.Equal(t, err, ErrReadHeaderFailed)
}

func TestReadErr_Payload(t *testing.T) {
	c1, c2 := net.Pipe()

	w := NewConnWriter(c2)
	r := NewConnReader(c1)
	go func() {
		w.WriteBuffer(NewBuffer(nil))

		var h Header
		binary.BigEndian.PutUint16(h[9:11], 10)
		c2.Write(h[:])
	}()

	var err error

	// 设置超时时间，加快超时
	r.SetReadTimeout(time.Millisecond * 100)

	_, err = r.ReadBuffer()
	assert.Nil(t, err)

	_, err = r.ReadBuffer()
	assert.Equal(t, err, ErrReadPayloadFailed)
}
