package stream

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

const (
	KB = 1024
	MB = 1024 * KB

	DefaultSplitSize = 63 * KB

	DIRECTION_LOCAL_TO_REMOTE = int(1)
	DIRECTION_REMOTE_TO_LOCAL = int(2)
)

var (
	// Flow Control Parameters (Global Defaults)
	DefaultBucketSize         int32         = 2 * MB
	DefaultBytesChanCap       int           = 4 * int(DefaultBucketSize) / DefaultSplitSize
	DefaultCloseAckTimeout    time.Duration = time.Second * 2
	DefaultAppendDataTimeout  time.Duration = time.Second * 2
	DefaultReadTimeout        time.Duration = time.Minute * 10
	DefaultWaitDataAckTimeout time.Duration = DefaultReadTimeout
)

type Stream struct {
	*Sender
	state    *State
	usedPort uint16 // 占用的端口。应当在Dial时进行绑定

	// for reader
	rmut           sync.RWMutex
	rclosed        bool
	bytesChan      chan []byte
	currBuf        []byte
	rDeadlineGuard *DeadlineGuard

	// for writer
	wmut           sync.Mutex
	wclosed        bool
	bucketSz       int32
	bucketEv       chan struct{}
	wDeadlineGuard *DeadlineGuard

	// for closer
	closeAckCh      chan struct{}
	closeAckTimeout time.Duration

	// variables
	appendDataTimeout  time.Duration
	readTimeout        time.Duration
	waitDataAckTimeout time.Duration

	logger *slog.Logger
}

func (s *Stream) GetState() *State {
	state := &State{}
	*state = *s.state
	return state
}

func (s *Stream) String() string {
	return fmt.Sprintf("[%v][%v,%v]", directionStr(s.state.Direction), s.state.LocalAddr.String(), s.state.RemoteAddr.String())
}

var streamIndex int32 = 0

func New(pwriter packet.Writer, initialWindowSize int32) *Stream {
	if initialWindowSize <= 0 {
		initialWindowSize = DefaultBucketSize
	}

	// Calculate chan cap based on window size
	chanCap := int(initialWindowSize) / DefaultSplitSize * 4
	if chanCap < 16 {
		chanCap = 16
	}

	state := &State{
		Index:   atomic.AddInt32(&streamIndex, 1),
		Created: time.Now(),
	}
	return &Stream{
		state:              state,
		Sender:             NewSender(pwriter, &state.WritedBufferCount),
		bytesChan:          make(chan []byte, chanCap),
		bucketSz:           initialWindowSize,
		bucketEv:           make(chan struct{}, 16),
		rDeadlineGuard:     &DeadlineGuard{},
		wDeadlineGuard:     &DeadlineGuard{},
		closeAckCh:         make(chan struct{}, 1),
		closeAckTimeout:    DefaultCloseAckTimeout,
		appendDataTimeout:  DefaultAppendDataTimeout,
		readTimeout:        DefaultReadTimeout,
		waitDataAckTimeout: DefaultWaitDataAckTimeout,
		logger:             slog.Default(),
	}
}

func NewDialStream(w packet.Writer,
	localDomain string, localIP, localPort uint16,
	remoteDomain string, remoteIP, remotePort uint16,
	initialWindowSize int32) *Stream {
	s := New(w, initialWindowSize)
	s.SetLocal(localIP, localPort)
	s.SetRemote(remoteIP, remotePort) // remoteIP有可能需要在接收到Ack时再补充调用
	s.SetUsedPort(localPort)
	s.state.Direction = DIRECTION_LOCAL_TO_REMOTE
	s.state.LocalDomain = localDomain
	s.state.RemoteDomain = remoteDomain
	return s
}

func NewAcceptStream(w packet.Writer,
	localDomain string, localIP, localPort uint16,
	remoteDomain string, remoteIP, remotePort uint16,
	initialWindowSize int32) *Stream {
	s := New(w, initialWindowSize)
	s.SetLocal(localIP, localPort)
	s.SetRemote(remoteIP, remotePort)
	s.state.Direction = DIRECTION_REMOTE_TO_LOCAL
	s.state.LocalDomain = localDomain
	s.state.RemoteDomain = remoteDomain
	return s
}

func (s *Stream) GetReadWriteSize() (int64, int64) {
	return s.state.ConnReadSize, s.state.ConnWriteSize
}

func (s *Stream) SetUsedPort(port uint16)       { s.usedPort = port }
func (s *Stream) GetUsedPort() uint16           { return s.usedPort }
func (s *Stream) SetRemoteDomain(domain string) { s.state.RemoteDomain = domain }
func (s *Stream) SetLogger(l *slog.Logger) {
	if l != nil {
		s.logger = l
	}
}

func directionStr(d int) string {
	if d == DIRECTION_LOCAL_TO_REMOTE {
		return "local-remote"
	}
	if d == DIRECTION_REMOTE_TO_LOCAL {
		return "remote-local"
	}
	return "invalid"
}
