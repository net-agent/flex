package packet

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func doTestWriteErr_Header(name string, t *testing.T, pc1, pc2 Conn) {
	c1 := pc1.GetRawConn()
	c2 := pc2.GetRawConn()
	go func() {
		buf := make([]byte, HeaderSz-1)

		// 未读完header就关闭，导致write错误
		io.ReadFull(c2, buf)
		c2.Close()
	}()

	w := NewConnWriter(c1)
	err := w.WriteBuffer(NewBuffer())
	assert.Equal(t, err, ErrWriteHeaderFailed)
}
func doTestWriteErr_Payload(name string, t *testing.T, pc1, pc2 Conn) {
	c1 := pc1.GetRawConn()
	c2 := pc2.GetRawConn()

	go func() {
		buf := make([]byte, HeaderSz+1)

		// 未读完header就关闭，导致write错误
		io.ReadFull(c2, buf)
		c2.Close()
	}()

	w := NewConnWriter(c1)
	pbuf := NewBuffer()
	pbuf.SetPayload([]byte("helloworld"))
	err := w.WriteBuffer(pbuf)
	assert.Equal(t, ErrWritePayloadFailed, err)
}
func doTestWriteErr_timeout(name string, t *testing.T, pc1, pc2 Conn) {
	pc1.SetWriteTimeout(time.Millisecond * 100)
	err := pc1.WriteBuffer(NewBufferWithCmd(0))
	assert.NotNil(t, err)
}

func TestWriter(t *testing.T) {
	pc1, pc2 := Pipe()
	doTestWriteErr_Header("test header write", t, pc1, pc2)

	pc3, pc4 := Pipe()
	doTestWriteErr_Payload("test payload write", t, pc3, pc4)

	c, _ := net.Pipe()
	pc := NewWithConn(c)
	doTestWriteErr_timeout("test timeout", t, pc, nil)
}

func TestWriteNil(t *testing.T) {
	pc1, _ := Pipe()
	pc3, _ := WsPipe()

	assert.Nil(t, pc1.WriteBuffer(nil))
	assert.Nil(t, pc3.WriteBuffer(nil))
}
