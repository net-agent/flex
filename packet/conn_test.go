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

func doTestDataTranasfer(name string, t *testing.T, pc1, pc2 Conn) {
	msg := []byte("hello world")
	buf := NewBuffer()
	buf.SetHeader(CmdCloseStream, 0, 1, 2, 3)
	buf.SetPayload(msg)

	t.Run(name, func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			recvBuf, err := pc1.ReadBuffer()
			assert.Nil(t, err)
			assert.EqualValues(t, recvBuf.Head, buf.Head)
			assert.EqualValues(t, recvBuf.Payload, buf.Payload)
		}()

		err := pc2.WriteBuffer(buf)
		assert.Nil(t, err)

		wg.Wait()
	})
}

func TestNetConn(t *testing.T) {
	pc1, pc2 := Pipe()
	pc3, pc4 := Pipe()
	doTestDataTranasfer("test Pipe", t, pc1, pc2)
	doTestDataTranasfer("test wsPipe", t, pc3, pc4)
}

func TestWebsocket(t *testing.T) {
	LOG_READ_BUFFER_HEADER = true
	LOG_WRITE_BUFFER_HEADER = true
	DefaultReadTimeout = time.Millisecond * 50

	msg := []byte("hello world")
	buf := NewBuffer()
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

		<-time.After(DefaultReadTimeout + time.Millisecond*100)
	})
}
