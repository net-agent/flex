package packet

import (
	"io"
	"testing"

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
	err := w.WriteBuffer(NewBuffer(nil))
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
	pbuf := NewBuffer(nil)
	pbuf.SetPayload([]byte("helloworld"))
	err := w.WriteBuffer(pbuf)
	assert.Equal(t, ErrWritePayloadFailed, err)
}

func TestWriter(t *testing.T) {
	pc1, pc2 := Pipe()
	doTestWriteErr_Header("test header write", t, pc1, pc2)
	pc3, pc4 := Pipe()
	doTestWriteErr_Payload("test payload write", t, pc3, pc4)
}
