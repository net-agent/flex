package packet

import (
	"bytes"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func equalBufTest(buf1, buf2 *Buffer, t *testing.T) bool {
	if !bytes.Equal(buf1.Head[:], buf2.Head[:]) {
		t.Error("head not equal")
		return false
	}
	if !bytes.Equal(buf1.Payload, buf2.Payload) {
		t.Error("payload not equal")
		return false
	}
	return true
}

func dataTransferTest(pc1 Reader, pc2 Writer, t *testing.T) {
	msg := []byte("hello world")
	buf := NewBuffer(nil)
	buf.SetHeader(CmdCloseStream, 0, 1, 2, 3)
	buf.SetPayload(msg)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		recvBuf, err := pc1.ReadBuffer()
		if err != nil {
			t.Error(err)
			return
		}
		if !equalBufTest(recvBuf, buf, t) {
			return
		}
	}()

	err := pc2.WriteBuffer(buf)
	if err != nil {
		t.Error(err)
		return
	}

	wg.Wait()
}

func TestNetConn(t *testing.T) {
	pc1, pc2 := Pipe()
	dataTransferTest(pc1, pc2, t)
}

func TestWebsocket(t *testing.T) {
	LOG_READ_BUFFER_HEADER = true
	LOG_WRITE_BUFFER_HEADER = true
	DefaultReadDeadline = time.Millisecond * 50

	msg := []byte("hello world")
	buf := NewBuffer(nil)
	buf.SetHeader(CmdCloseStream, 0, 1, 2, 3)
	buf.SetPayload(msg)

	addr := "localhost:12003"
	upgrader := websocket.Upgrader{}
	http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()

		pc := NewWithWs(c)
		recvBuf, err := pc.ReadBuffer()
		if err == nil {
			pc.WriteBuffer(recvBuf)
		}
	})
	go http.ListenAndServe(addr, nil)

	<-time.After(time.Millisecond * 100)

	//
	// client side code
	//
	u := url.URL{Scheme: "ws", Host: addr, Path: "/echo"}

	t.Run("websocket ok test case", func(t *testing.T) {
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		assert.Nil(t, err)
		defer c.Close()
		pc := NewWithWs(c)

		err = pc.WriteBuffer(buf)
		assert.Nil(t, err)

		recvBuf, err := pc.ReadBuffer()
		assert.Nil(t, err)
		assert.True(t, bytes.Equal(buf.Head[:], recvBuf.Head[:]))
		assert.True(t, bytes.Equal(buf.Payload, recvBuf.Payload))
	})

	t.Run("websocket ErrBadDataType", func(t *testing.T) {
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		assert.Nil(t, err)
		defer c.Close()

		c.WriteMessage(websocket.TextMessage, []byte("hello world"))
	})

	t.Run("websocket ReadMessage timeout", func(t *testing.T) {
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		assert.Nil(t, err)
		defer c.Close()

		<-time.After(DefaultReadDeadline + time.Millisecond*100)
	})
}
