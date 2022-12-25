package stream

import (
	"bytes"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/stretchr/testify/assert"
)

func TestPipe(t *testing.T) {
	payload := make([]byte, 1024*1024*10)
	rand.Read(payload)

	s1, s2 := Pipe()

	log.Println(s1.String(), s1.GetState().String())
	log.Println(s2.String(), s2.GetState().String())

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		_, err := s1.Write(payload)
		assert.Nil(t, err)

		err = s1.Close()
		assert.Nil(t, err)

		<-time.After(time.Millisecond * 100)
	}()

	buf := make([]byte, len(payload))
	_, err := io.ReadFull(s2, buf)
	assert.Nil(t, err)
	assert.True(t, bytes.Equal(payload, buf))

	wg.Wait()
}

func TestReadAndRoutePbufFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewWithConn(c)
	pc.SetReadTimeout(time.Millisecond * 100)

	readAndRoutePbuf(nil, nil, pc)
}

func TestRouteErr(t *testing.T) {
	badPbuf := packet.NewBufferWithCmd(0)
	cmdCh := make(chan *packet.Buffer, 10)
	ackCh := make(chan *packet.Buffer, 10)

	cmdCh <- badPbuf
	ackCh <- badPbuf
	go routeCmd(nil, cmdCh)
	go routeAck(nil, ackCh)

	close(cmdCh)
	close(ackCh)
}
