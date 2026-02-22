package node

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
)

var (
	ErrListenPortIsUsed      = errors.New("listen port is used")
	ErrListenerClosed        = errors.New("listener closed")
	ErrListenerNotFound      = errors.New("listener not found")
	ErrConvertListenerFailed = errors.New("convert listener failed")
)

type ListenHub struct {
	host      *Node
	portm     *idpool.Pool
	listeners sync.Map // map[port]*Listener
}

func (hub *ListenHub) init(host *Node, portm *idpool.Pool) {
	hub.host = host
	hub.portm = portm
}

func (hub *ListenHub) Listen(port uint16) (net.Listener, error) {
	listener := &Listener{
		hub:     hub,
		port:    port,
		streams: make(chan *stream.Stream, 32),
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

func (hub *ListenHub) getListenerByPort(port uint16) (*Listener, error) {
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

func (hub *ListenHub) closeAllListeners() {
	hub.listeners.Range(func(key, value interface{}) bool {
		if l, ok := value.(*Listener); ok {
			l.Close()
		}
		return true
	})
}

func (hub *ListenHub) getActiveListeners() []*Listener {
	list := make([]*Listener, 0)
	hub.listeners.Range(func(key, value interface{}) bool {
		if l, ok := value.(*Listener); ok {
			list = append(list, l)
		}
		return true
	})
	return list
}

// 处理对端发送过来的OpenStream请求
func (hub *ListenHub) handleCmdOpenStream(pbuf *packet.Buffer) {
	var negotiatedWindowSize int32
	var ackMessage string

	defer func() {
		var ack packet.OpenStreamACK
		if ackMessage == "" {
			ack = packet.OpenStreamACK{OK: true, WindowSize: uint32(negotiatedWindowSize)}
		} else {
			ack = packet.OpenStreamACK{Error: ackMessage}
		}

		_ = pbuf.SetPayload(ack.Encode())
		pbuf.SetCmd(packet.AckOpenStream)

		srcIP, srcPort := pbuf.SrcIP(), pbuf.SrcPort()
		distIP, distPort := pbuf.DistIP(), pbuf.DistPort()
		pbuf.SetSrc(distIP, distPort)
		pbuf.SetDist(srcIP, srcPort)

		err := hub.host.WriteBuffer(pbuf)
		if err != nil {
			hub.host.logger.Warn("write ack-msg failed", "error", err)
		}
	}()

	// 找到该端口是否存在listener，如果不存在，则说明此端口并未开放
	l, err := hub.getListenerByPort(pbuf.DistPort())
	if err != nil {
		ackMessage = err.Error()
		return
	}

	req := packet.DecodeOpenStreamRequest(pbuf.Payload)
	remoteDomain := req.Domain
	remoteWindowSize := int32(req.WindowSize)

	if remoteDomain == "" && pbuf.SrcIP() == hub.host.ip {
		remoteDomain = hub.host.domain
	}

	// Negotiate Window Size
	localWindowSize := hub.host.GetWindowSize()
	negotiatedWindowSize = localWindowSize // Default to local
	if remoteWindowSize > 0 {
		if localWindowSize <= 0 || remoteWindowSize < localWindowSize {
			negotiatedWindowSize = remoteWindowSize
		}
	}
	// If both are 0, NewAcceptStream will handle default

	s := stream.NewAcceptStream(
		hub.host,
		hub.host.domain, pbuf.DistIP(), pbuf.DistPort(),
		remoteDomain, pbuf.SrcIP(), pbuf.SrcPort(),
		negotiatedWindowSize,
	)
	defer func() {
		if s != nil {
			s.Close()
		}
	}()

	// ack发出后理论上就会马上有数据从对端发送过来
	// 因此需要先完成stream和sid的绑定，然后再应答ack，避免因时序问题出现的数据包丢失
	err = hub.host.attachStream(s, pbuf.SID())
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
	hub     *ListenHub
	streams chan *stream.Stream

	closed bool
	locker sync.RWMutex
	str    string
}

func (l *Listener) Accept() (net.Conn, error) {
	l.locker.RLock()
	closed := l.closed
	l.locker.RUnlock()

	if closed {
		return nil, ErrListenerClosed
	}
	s := <-l.streams
	if s == nil {
		return nil, ErrListenerClosed
	}
	return s, nil
}

func (l *Listener) Close() error {
	l.locker.Lock()
	defer l.locker.Unlock()

	if l.closed {
		return ErrListenerClosed
	}

	l.closed = true
	l.hub.listeners.Delete(l.port)
	close(l.streams)
	return nil
}

func (l *Listener) Addr() net.Addr  { return l }
func (l *Listener) Network() string { return "flex" }
func (l *Listener) String() string  { return l.str }
