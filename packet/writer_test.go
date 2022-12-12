package packet

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteErr_Header(t *testing.T) {
	c1, c2 := net.Pipe()

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
func TestWriteErr_Payload(t *testing.T) {
	c1, c2 := net.Pipe()

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
	assert.Equal(t, err, ErrWritePayloadFailed)
}
