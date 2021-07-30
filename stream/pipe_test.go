package stream

import (
	"bytes"
	"io"
	"math/rand"
	"sync"
	"testing"
)

func TestPipe(t *testing.T) {
	s1, s2 := Pipe()

	payload := make([]byte, 1024*1024*10)
	rand.Read(payload)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer s1.Close()
		_, err := s1.Write(payload)
		if err != nil {
			t.Error(err)
			return
		}
	}()

	buf := make([]byte, len(payload))
	_, err := io.ReadFull(s2, buf)
	if err != nil {
		t.Error(err)
		return
	}

	if !bytes.Equal(payload, buf) {
		t.Error("payload not equal")
		return
	}

	wg.Wait()
}
