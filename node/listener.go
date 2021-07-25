package node

import (
	"errors"
	"fmt"
	"net"

	"github.com/net-agent/flex/stream"
)

type Listener struct {
	port         uint16
	node         *Node
	openedStream chan *stream.Conn
	closed       bool
	str          string
}

func (n *Node) Listen(port uint16) (net.Listener, error) {
	listener := &Listener{
		port:         port,
		node:         n,
		openedStream: make(chan *stream.Conn, 1024),
		closed:       false,
		str:          fmt.Sprintf("%v:%v", n.GetIP(), port),
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
	s := <-l.openedStream
	if s == nil {
		return nil, errors.New("unexpected nil stream")
	}
	return s, nil
}

func (l *Listener) Close() error {
	if l.closed {
		return errors.New("close on closed listener")
	}
	l.node.listenPorts.Delete(l.port)
	close(l.openedStream)
	l.node.freePorts <- l.port
	return nil
}

func (l *Listener) Addr() net.Addr  { return l }
func (l *Listener) Network() string { return "flex" }
func (l *Listener) String() string  { return l.str }

func (l *Listener) AppendConn(s *stream.Conn) {
	if l.closed {
		go s.Close()
	} else {
		l.openedStream <- s
	}
}
