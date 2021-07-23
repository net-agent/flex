package node

import "github.com/net-agent/flex/stream"

type Listener struct {
	openedStream chan *stream.Conn
}

func (l *Listener) Accept() {}
