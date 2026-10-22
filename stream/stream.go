package stream

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Direction int

const (
	KB = 1024
	MB = 1024 * KB

	DefaultSplitSize = 16 * KB

	// MaxWindowSize 是协商窗口的硬上限。
	// 窗口值来自对端应答，若不钳制，恶意对端可返回 0x7FFFFFFF 迫使本端
	// 按窗口大小分配接收缓冲（每流可达数百 MB）。合法对端只会返回
	// min(双方本地窗口)，因此该上限不影响正常协商。
	MaxWindowSize int32 = 16 * MB

	DirectionOutbound Direction = 1 // local → remote
	DirectionInbound  Direction = 2 // remote → local
)

var (
	// Flow Control Parameters (Global Defaults)
	DefaultWindowSize      int32         = 2 * MB
	DefaultCloseAckTimeout time.Duration = time.Second * 2
)

type Stream struct {
	sender    *sender
	state     *State
	stateMu   sync.RWMutex // protects non-atomic state fields
	boundPort uint16       // 占用的端口。应当在Dial时进行绑定

	// for reader
	rchanMu      sync.RWMutex // 保护 readClosed 状态位（供观测与幂等关闭）
	readClosed   bool
	recv         *recvBuffer
	readMu       sync.Mutex // 序列化 Read 调用（net.Conn 契约）
	readDeadline *DeadlineGuard

	// for ack batching
	acks     *ackTracker
	ackOnce  sync.Once
	failOnce sync.Once

	// for writer
	writeCallMu   sync.Mutex // serializes Write call-order for stream-ordered semantics
	writeMu       sync.Mutex // serializes write/CloseWrite, protects writeClosed and closeCh
	writeClosed   bool
	window        *WindowGuard
	writeDeadline *DeadlineGuard

	// for closer
	closeCh         chan struct{} // closed when CloseWrite is called, to interrupt blocked Write
	closeAckCh      chan struct{}
	closeAckTimeout time.Duration

	// lifecycle hook: called once when both read+write are closed.
	closeMask  atomic.Uint32
	detachDone atomic.Bool
	onDetachMu sync.RWMutex
	onDetachFn func()

	logger *slog.Logger
}

func (s *Stream) GetState() *State {
	st := &State{}

	s.stateMu.RLock()
	st.Index = s.state.Index
	st.IsClosed = s.state.IsClosed
	st.Created = s.state.Created
	st.Closed = s.state.Closed
	st.CloseReason = s.state.CloseReason
	st.Direction = s.state.Direction
	st.LocalDomain = s.state.LocalDomain
	st.LocalAddr = s.state.LocalAddr
	st.RemoteDomain = s.state.RemoteDomain
	st.RemoteAddr = s.state.RemoteAddr
	s.stateMu.RUnlock()

	s.rchanMu.RLock()
	st.ReadClosed = s.readClosed
	s.rchanMu.RUnlock()

	s.writeMu.Lock()
	st.WriteClosed = s.writeClosed
	s.writeMu.Unlock()

	st.SentBufferCount = atomic.LoadInt32(&s.state.SentBufferCount)
	st.RecvBufferCount = atomic.LoadInt32(&s.state.RecvBufferCount)
	st.RecvDataSize = atomic.LoadInt64(&s.state.RecvDataSize)
	st.RecvAckTotal = atomic.LoadInt64(&s.state.RecvAckTotal)
	st.SentAckTotal = atomic.LoadInt64(&s.state.SentAckTotal)
	st.BytesRead = atomic.LoadInt64(&s.state.BytesRead)
	st.BytesWritten = atomic.LoadInt64(&s.state.BytesWritten)

	return st
}

func (s *Stream) String() string {
	return fmt.Sprintf("[%v][%v,%v]", directionStr(s.state.Direction), s.state.LocalAddr.String(), s.state.RemoteAddr.String())
}

var streamIndex int32 = 0

func New(pwriter Writer, initialWindowSize int32) *Stream {
	if initialWindowSize <= 0 {
		initialWindowSize = DefaultWindowSize
	}

	state := &State{
		Index:   atomic.AddInt32(&streamIndex, 1),
		Created: time.Now(),
	}
	s := &Stream{
		state:           state,
		recv:            newRecvBuffer(initialWindowSize),
		window:          NewWindowGuard(initialWindowSize, 16),
		readDeadline:    &DeadlineGuard{},
		writeDeadline:   &DeadlineGuard{},
		closeCh:         make(chan struct{}),
		closeAckCh:      make(chan struct{}, 1),
		closeAckTimeout: DefaultCloseAckTimeout,
		logger:          slog.Default(),
	}
	s.sender = newSender(pwriter, &state.SentBufferCount)
	s.acks = newAckTracker(s.sender.SendDataAck, func(n int64) {
		atomic.AddInt64(&s.state.SentAckTotal, n)
	}, s.logger)
	return s
}

func NewDialStream(w Writer,
	localDomain string, localIP, localPort uint16,
	remoteDomain string, remoteIP, remotePort uint16,
	initialWindowSize int32) *Stream {
	s := New(w, initialWindowSize)
	s.setLocal(localIP, localPort)
	s.setRemote(remoteIP, remotePort) // remoteIP有可能需要在接收到Ack时再补充调用
	s.SetBoundPort(localPort)
	s.state.Direction = DirectionOutbound
	s.state.LocalDomain = localDomain
	s.state.RemoteDomain = remoteDomain
	return s
}

func NewAcceptStream(w Writer,
	localDomain string, localIP, localPort uint16,
	remoteDomain string, remoteIP, remotePort uint16,
	initialWindowSize int32) *Stream {
	s := New(w, initialWindowSize)
	s.setLocal(localIP, localPort)
	s.setRemote(remoteIP, remotePort)
	s.state.Direction = DirectionInbound
	s.state.LocalDomain = localDomain
	s.state.RemoteDomain = remoteDomain
	return s
}

func (s *Stream) GetReadWriteSize() (int64, int64) {
	return atomic.LoadInt64(&s.state.BytesRead), atomic.LoadInt64(&s.state.BytesWritten)
}

func (s *Stream) SetBoundPort(port uint16) { s.boundPort = port }
func (s *Stream) GetBoundPort() uint16     { return s.boundPort }
func (s *Stream) SetRemoteDomain(domain string) {
	s.stateMu.Lock()
	s.state.RemoteDomain = domain
	s.stateMu.Unlock()
}
func (s *Stream) SetLogger(l *slog.Logger) {
	if l != nil {
		s.logger = l
		if s.acks != nil {
			s.acks.logger = l
		}
	}
}

func directionStr(d Direction) string {
	if d == DirectionOutbound {
		return "local-remote"
	}
	if d == DirectionInbound {
		return "remote-local"
	}
	return "invalid"
}
