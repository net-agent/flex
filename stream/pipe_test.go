package stream

import (
	"bytes"
	"io"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPipe(t *testing.T) {
	payload := make([]byte, 1024*1024*10)
	rand.Read(payload)

	s1, s2 := Pipe()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		_, err := s1.Write(payload)
		assert.Nil(t, err)

		err = s1.Close()
		assert.Nil(t, err)
	}()

	buf := make([]byte, len(payload))
	_, err := io.ReadFull(s2, buf)
	assert.Nil(t, err)
	assert.True(t, bytes.Equal(payload, buf))

	wg.Wait()
}
