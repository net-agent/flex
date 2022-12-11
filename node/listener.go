package node

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

var (
	ErrListenPortIsUsed      = errors.New("listen port is used")
	ErrListenerClosed        = errors.New("listener closed")
	ErrListenerNotFound      = errors.New("listener not found")
	ErrConvertListenerFailed = errors.New("convert listener failed")
)

type ListenerHub struct {
	host      *Node
	listeners sync.Map // map[port]*Listener
}

func (hub *ListenerHub) Init(host *Node) {
	hub.host = host
}

func (hub *ListenerHub) Listen(port uint16) (net.Listener, error) {
	listener := &Listener{
		hub:     hub,
		port:    port,
		streams: make(chan *stream.Conn, 32),
		closed:  false,
		str:     fmt.Sprintf("%v:%v", hub.host.GetIP(), port),
	}

	_, loaded := hub.listeners.LoadOrStore(port, listener)
	if loaded {
		close(listener.streams)
		return nil, ErrListenPortIsUsed
	}

	return listener, nil
}

func (hub *ListenerHub) GetListenerByPort(port uint16) (*Listener, error) {
	it, found := hub.listeners.Load(port)
	if !found {
		return nil, ErrListenerNotFound
	}
	l, ok := it.(*Listener)
	if !ok {
		return nil, ErrConvertListenerFailed
	}
	return l, nil
}

// 处理对端发送过来的OpenStream请求
func (hub *ListenerHub) HandleCmdOpenStream(pbuf *packet.Buffer) {
	ackMessage := ""
	defer func() {
		err := hub.host.WriteBuffer(pbuf.SetOpenACK(ackMessage))
		if err != nil {
			log.Printf("HandleCmdOpenStream write ackMessage failed, err=%v\n", err)
		}
	}()

	// 找到该端口是否存在listener，如果不存在，则说明此端口并未开放
	l, err := hub.GetListenerByPort(pbuf.DistPort())
	if err != nil {
		ackMessage = err.Error()
		return
	}

	// 根据OpenCmd的信息创建stream
	s := stream.New(false)
	s.SetLocal(pbuf.DistIP(), pbuf.DistPort())
	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
	s.SetDialer(string(pbuf.Payload))
	s.InitWriter(hub.host)
	defer func() {
		if s != nil {
			s.Close()
		}
	}()

	// ack发出后理论上就会马上有数据从对端发送过来
	// 因此需要先完成stream和sid的绑定，然后再应答ack，避免因时序问题出现的数据包丢失
	err = hub.host.AttachStream(s, pbuf.SID())
	if err != nil {
		ackMessage = err.Error()
		return
	}

	// todo: maybe error with push channel
	l.streams <- s
	s = nil
}

// 实现net.Listener的协议
type Listener struct {
	port    uint16
	hub     *ListenerHub
	streams chan *stream.Conn

	closed bool
	locker sync.RWMutex
	str    string
}

// func (n *Node) Listen(port uint16) (net.Listener, error) {
// 	listener := &Listener{
// 		port:    port,
// 		host:    n,
// 		streams: make(chan *stream.Conn, 1024),
// 		closed:  false,
// 		str:     fmt.Sprintf("%v:%v", n.GetIP(), port),
// 	}

// 	_, loaded := n.listenPorts.LoadOrStore(port, listener)
// 	if loaded {
// 		return nil, errors.New("port busy now")
// 	}

// 	return listener, nil
// }

func (l *Listener) Accept() (net.Conn, error) {
	if l.closed {
		return nil, errors.New("accept on closed lisntener")
	}
	s := <-l.streams
	if s == nil {
		return nil, errors.New("unexpected nil stream")
	}
	return s, nil
}

func (l *Listener) Close() error {
	l.locker.Lock()
	defer l.locker.Unlock()

	if l.closed {
		return errors.New("close on closed listener")
	}

	l.closed = true
	l.hub.listeners.Delete(l.port)
	close(l.streams)
	l.hub.host.portm.ReleaseNumberSrc(l.port)
	return nil
}

func (l *Listener) Addr() net.Addr  { return l }
func (l *Listener) Network() string { return "flex" }
func (l *Listener) String() string  { return l.str }
