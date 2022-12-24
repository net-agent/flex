package packet

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var wsPipeInst *wsPiper

func init() {
	wsPipeInst = &wsPiper{}
	wsPipeInst.Init()
	go wsPipeInst.Run()
}

func Pipe() (Conn, Conn) {
	c1, c2 := net.Pipe()
	return NewWithConn(c1), NewWithConn(c2)
}

func WsPipe() (Conn, Conn) {
	return wsPipeInst.Pipe()
}

type wsPiper struct {
	pcChan     chan Conn
	listenChan chan net.Conn
	dialer     *websocket.Dialer
	upgrader   *websocket.Upgrader
	locker     sync.Mutex
}

func (wp *wsPiper) Init() {
	wp.pcChan = make(chan Conn, 10)
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

func (wp *wsPiper) Run() {
	svr := &http.Server{
		Handler: wp,
	}
	svr.Serve(wp)
}

func (wp *wsPiper) Pipe() (Conn, Conn) {
	wp.locker.Lock()
	defer wp.locker.Unlock()

	c, _, _ := wp.dialer.Dial("ws://pipe.com:1", nil)
	pc1 := NewWithWs(c)
	pc2 := <-wp.pcChan

	pc1.SetReadTimeout(DefaultReadTimeout)
	pc1.SetWriteTimeout(DefaultWriteTimeout)
	pc2.SetReadTimeout(DefaultReadTimeout)
	pc2.SetWriteTimeout(DefaultWriteTimeout)

	return pc1, pc2
}

func (wp *wsPiper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, _ := wp.upgrader.Upgrade(w, r, nil)
	wp.pcChan <- NewWithWs(c)
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
