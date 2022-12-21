package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloseErr(t *testing.T) {
	s1, s2 := Pipe()
	err := s1.Close()
	assert.Nil(t, err)

	err = s1.Close()
	assert.NotNil(t, err)

	err = s2.CloseRead()
	assert.Equal(t, ErrReaderIsClosed, err)

	err = s2.CloseWrite()
	assert.Equal(t, ErrWriterIsClosed, err)
}
