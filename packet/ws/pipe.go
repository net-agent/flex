package ws

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet"
)

var pipeInst *wsPiper

func init() {
	pipeInst = &wsPiper{}
	pipeInst.init()
	go pipeInst.run()
}

// Pipe creates a pair of packet.Conn backed by an in-process WebSocket connection.
func Pipe() (packet.Conn, packet.Conn) {
	return pipeInst.pipe()
}

type wsPiper struct {
	pcChan     chan packet.Conn
	listenChan chan net.Conn
	dialer     *websocket.Dialer
	upgrader   *websocket.Upgrader
	locker     sync.Mutex
}

func (wp *wsPiper) init() {
	wp.pcChan = make(chan packet.Conn, 10)
	wp.listenChan = make(chan net.Conn, 10)
	wp.dialer = &websocket.Dialer{
		HandshakeTimeout: time.Second * 5,
		NetDial: func(network, addr string) (net.Conn, error) {
			c1, c2 := net.Pipe()
			wp.listenChan <- c2
			return c1, nil
		},
	}
	wp.upgrader = &websocket.Upgrader{}
}

func (wp *wsPiper) run() {
	svr := &http.Server{Handler: wp}
	svr.Serve(wp)
}

func (wp *wsPiper) pipe() (packet.Conn, packet.Conn) {
	wp.locker.Lock()
	defer wp.locker.Unlock()

	c, _, _ := wp.dialer.Dial("ws://pipe.com:1", nil)
	pc1 := NewConn(c)
	pc2 := <-wp.pcChan

	pc1.SetReadTimeout(packet.DefaultReadTimeout)
	pc1.SetWriteTimeout(packet.DefaultWriteTimeout)
	pc2.SetReadTimeout(packet.DefaultReadTimeout)
	pc2.SetWriteTimeout(packet.DefaultWriteTimeout)

	return pc1, pc2
}

func (wp *wsPiper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, _ := wp.upgrader.Upgrade(w, r, nil)
	wp.pcChan <- NewConn(c)
}

func (wp *wsPiper) Accept() (net.Conn, error) {
	conn := <-wp.listenChan
	return conn, nil
}

func (wp *wsPiper) Close() error {
	close(wp.listenChan)
	return nil
}

func (wp *wsPiper) Addr() net.Addr  { return wp }
func (wp *wsPiper) Network() string { return "" }
func (wp *wsPiper) String() string  { return "" }
