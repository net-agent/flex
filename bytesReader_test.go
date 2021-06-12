package flex

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestStreamInterfaces(t *testing.T) {
	var _ net.Conn = &Stream{}
}

func TestBytesPipeReadWrite(t *testing.T) {
	pipe := NewBytesPipe()
	bigData := make([]byte, 1024*16)
	dataSz := len(bigData)
	readWriteTest := func(writeSize int, writeTick time.Duration, readSize int, log bool) {
		defer func() {
			if log {
				fmt.Println("test done")
			}
		}()
		var wg sync.WaitGroup

		// slow write
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(writeTick)
			start := 0
			end := 0
			size := writeSize

			for {
				select {
				case <-ticker.C:
					end = start + size
					if end > dataSz {
						end = dataSz
					}
					err := pipe.append(bigData[start:end])
					if err != nil {
						t.Error(err)
						return
					}
					if log {
						fmt.Printf("append data size: %v\n", end)
					}
					start = end

					if start >= dataSz {
						return
					}
				}
			}
		}()

		// fast read
		wg.Add(1)
		go func() {
			defer wg.Done()
			readed := []byte{}
			var buf []byte
			for len(readed) < dataSz {
				if len(readed)+readSize > dataSz {
					buf = make([]byte, dataSz-len(readed))
				} else {
					buf = make([]byte, readSize)
				}
				n, err := io.ReadFull(pipe, buf)
				if n > 0 {
					readed = append(readed, buf[:n]...)
					if log {
						fmt.Printf("read data size: %v\n", len(readed))
					}
				}
				if err != nil {
					if err != io.EOF {
						t.Error(err)
						return
					}
					break
				}
			}

			if !bytes.Equal(readed, bigData) {
				t.Error("not equal")
				return
			}
		}()

		wg.Wait()
	}

	readWriteTest(1024, time.Millisecond*100, 1024*5, true)
	readWriteTest(88, time.Millisecond*1, 1099, false)
	readWriteTest(1024, time.Millisecond*1, 12, false)
}

func TestBytesPipeClose(t *testing.T) {
	pipe := NewBytesPipe()
	payload := []byte("hello world")

	err := pipe.append(payload)
	if err != nil {
		t.Error(err)
		return
	}

	buf := make([]byte, len(payload)-2)
	n, err := io.ReadFull(pipe, buf)
	if n != len(buf) {
		t.Error("read size failed")
		return
	}
	if err != nil {
		t.Error(err)
		return
	}

	if !bytes.Equal(buf, payload[:len(buf)]) {
		t.Error("not equal")
		return
	}

	//
	// close test
	//
	pipe.Close()
	err = pipe.Close()
	if err == nil {
		t.Error("unexpected nil error")
		return
	}

	err = pipe.append(nil)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	err = pipe.append(payload)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}

	buf2 := make([]byte, len(payload)-len(buf))
	n, err = io.ReadFull(pipe, buf2)
	if n != len(buf2) {
		t.Error("unexpected read n")
		return
	}
	if err != nil {
		t.Error(err)
		return
	}

	_, err = pipe.Read(buf)
	if err != io.EOF {
		t.Error("unexpected error", err)
		return
	}

	_, err = pipe.Read(buf)
	if err == nil || err == io.EOF {
		t.Error("uexpeceted error")
		return
	}
}
