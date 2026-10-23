package node

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/net-agent/flex/v3/wire"
)

var (
	ErrListenPortIsUsed      = errors.New("listen port is used")
	ErrListenerClosed        = errors.New("listener closed")
	ErrListenerNotFound      = errors.New("listener not found")
	ErrConvertListenerFailed = errors.New("convert listener failed")
	ErrListenerBacklogFull   = errors.New("listener backlog full")

	listenerEnqueueTimeout = time.Second
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
		done:    make(chan struct{}),
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

// handleOpenFrame processes an OpenStream control frame. It is a control-lane
// handler: the dispatcher releases f after it returns.
func (hub *ListenHub) handleOpenFrame(f *wire.Frame) error {
	var (
		negotiatedWindowSize int32
		ackMessage           string
		s                    *stream.Stream
	)

	defer func() {
		if s != nil {
			s.Close()
		}
		var ack packet.OpenStreamACK
		if ackMessage == "" {
			ack = packet.OpenStreamACK{OK: true, WindowSize: uint32(negotiatedWindowSize)}
		} else {
			ack = packet.OpenStreamACK{Error: ackMessage}
		}
		reply, err := wire.NewControl(wire.AckOpenStream, f.Dst(), f.Src(), ack.Encode())
		if err != nil {
			hub.host.logger.Warn("build ack-msg failed", "error", err)
			return
		}
		if err := hub.host.SendFrame(reply); err != nil {
			hub.host.logger.Warn("write ack-msg failed", "error", err)
		}
	}()

	// 找到该端口是否存在 listener；不存在说明端口未开放。
	l, err := hub.getListenerByPort(f.Dst().Port)
	if err != nil {
		ackMessage = err.Error()
		return nil
	}

	req := packet.DecodeOpenStreamRequest(f.Payload())
	remoteDomain := req.Domain
	if remoteDomain == "" && f.Src().IP == hub.host.ip {
		remoteDomain = hub.host.domain
	}

	// Negotiate Window Size：双方都使用确定值，取较小者。
	// 对端请求值超过硬上限时按上限处理，防止恶意请求撑大本端发送窗口。
	localWindowSize := hub.host.effectiveWindowSize()
	negotiatedWindowSize = localWindowSize
	if req.WindowSize > 0 {
		remoteWindowSize := req.WindowSize
		if remoteWindowSize > uint32(stream.MaxWindowSize) {
			remoteWindowSize = uint32(stream.MaxWindowSize)
		}
		if int32(remoteWindowSize) < localWindowSize {
			negotiatedWindowSize = int32(remoteWindowSize)
		}
	}

	s = stream.NewAcceptStreamWithEndpoint(
		hub.host,
		hub.host.domain, f.Dst().IP, f.Dst().Port,
		remoteDomain, f.Src().IP, f.Src().Port,
		negotiatedWindowSize,
	)

	// ack 发出后对端就会马上发数据，因此先完成 stream 和 sid 的绑定，
	// 再应答 ack，避免时序问题导致数据包丢失。
	if err := hub.host.streamHub.attachStream(s, f.SID().Uint64()); err != nil {
		ackMessage = err.Error()
		return nil
	}

	timer := time.NewTimer(listenerEnqueueTimeout)
	defer timer.Stop()

	select {
	case l.streams <- s:
		s = nil
	case <-l.done:
		ackMessage = ErrListenerClosed.Error()
	case <-timer.C:
		ackMessage = ErrListenerBacklogFull.Error()
	}
	return nil
}

// handleCmdOpenStream is the legacy packet.Buffer adapter.
func (hub *ListenHub) handleCmdOpenStream(pbuf *packet.Buffer) {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return
	}
	_ = hub.handleOpenFrame(f)
	f.Release()
}

// Listener 实现 net.Listener 协议
type Listener struct {
	port    uint16
	hub     *ListenHub
	streams chan *stream.Stream
	done    chan struct{}

	closed bool
	locker sync.RWMutex
	str    string
}

func (l *Listener) Accept() (net.Conn, error) {
	select {
	case s := <-l.streams:
		if s == nil {
			return nil, ErrListenerClosed
		}
		return s, nil
	case <-l.done:
		return nil, ErrListenerClosed
	}
}

func (l *Listener) Close() error {
	l.locker.Lock()
	defer l.locker.Unlock()

	if l.closed {
		return ErrListenerClosed
	}

	l.closed = true
	l.hub.listeners.Delete(l.port)
	close(l.done)
	return nil
}

func (l *Listener) Addr() net.Addr  { return l }
func (l *Listener) Network() string { return "flex" }
func (l *Listener) String() string  { return l.str }
