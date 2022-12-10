package node

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

var (
	ErrListenerClosed = errors.New("listener closed")
)

type Listener struct {
	port    uint16
	host    *Node
	streams chan *stream.Conn
	closed  bool
	locker  sync.RWMutex
	str     string
}

func (n *Node) Listen(port uint16) (net.Listener, error) {
	listener := &Listener{
		port:    port,
		host:    n,
		streams: make(chan *stream.Conn, 1024),
		closed:  false,
		str:     fmt.Sprintf("%v:%v", n.GetIP(), port),
	}

	_, loaded := n.listenPorts.LoadOrStore(port, listener)
	if loaded {
		return nil, errors.New("port busy now")
	}

	return listener, nil
}

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
	l.host.listenPorts.Delete(l.port)
	close(l.streams)
	l.host.portm.ReleaseNumberSrc(l.port)
	return nil
}

func (l *Listener) Addr() net.Addr  { return l }
func (l *Listener) Network() string { return "flex" }
func (l *Listener) String() string  { return l.str }

func (l *Listener) HandleCmdOpenStream(pbuf *packet.Buffer) error {
	l.locker.RLock()
	defer l.locker.RUnlock()
	if l.closed {
		return ErrListenerClosed
	}

	// 根据OpenCmd的信息创建stream
	s := stream.New(false)
	s.SetLocal(l.host.GetIP(), l.port)
	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
	s.SetDialer(string(pbuf.Payload))
	s.InitWriter(l.host)
	defer func() {
		if s != nil {
			s.Close()
		}
	}()

	err := l.host.AttachStream(s, pbuf.SID())
	if err != nil {
		return err
	}

	// todo: maybe error with push channel
	l.streams <- s
	s = nil

	return nil
}
