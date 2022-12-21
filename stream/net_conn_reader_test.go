package stream

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConnReader(t *testing.T) {
	s1, s2 := Pipe()

	buf := []byte{1}
	s1.readTimeout = time.Millisecond * 100
	_, err := io.ReadFull(s1, buf)
	assert.Equal(t, ErrReadFromStreamTimeout, err)

	s2.bucketSz = 1
	s2.waitDataAckTimeout = time.Millisecond * 100
	_, err = s2.Write([]byte{1, 2, 3})
	assert.Equal(t, ErrWaitForDataAckTimeout, err)

	err = s2.Close()
	assert.Nil(t, err)

	_, err = s2.Read(buf)
	assert.Equal(t, io.EOF, err)

	_, err = s1.Write([]byte{1, 2, 3})
	assert.Equal(t, ErrWriterIsClosed, err)
}
