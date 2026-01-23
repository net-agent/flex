package stream

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/warning"
)

const (
	KB                  = 1024
	MB                  = 1024 * KB
	DefaultBucketSize   = 2 * MB
	DefaultSplitSize    = 63 * KB
	DefaultBytesChanCap = 4 * DefaultBucketSize / DefaultSplitSize

	DefaultCloseAckTimeout    = time.Second * 2
	DefaultAppendDataTimeout  = time.Second * 2  // 此参数设置过小会导致丢包。过大会导致全局阻塞
	DefaultReadTimeout        = time.Minute * 10 // 此参数设置过小会导致长连接容易断开
	DefaultWaitDataAckTimeout = DefaultReadTimeout

	// sid被重置后，多久恢复可用
	// 时间过短的时间可能会导致失效状态包清理不干净
	// 时间过长则容易引起正常连接复用端口失败
	DefaultResetSIDKeepTime = time.Minute * 3

	DIRECTION_LOCAL_TO_REMOTE = int(1)
	DIRECTION_REMOTE_TO_LOCAL = int(2)
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

	warning.Guard
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

func New(pwriter packet.Writer) *Stream {
	state := &State{
		Index:   atomic.AddInt32(&streamIndex, 1),
		Created: time.Now(),
	}
	return &Stream{
		state:              state,
		Sender:             NewSender(pwriter, &state.WritedBufferCount),
		bytesChan:          make(chan []byte, DefaultBytesChanCap),
		bucketSz:           DefaultBucketSize,
		bucketEv:           make(chan struct{}, 16),
		rDeadlineGuard:     &DeadlineGuard{},
		wDeadlineGuard:     &DeadlineGuard{},
		closeAckCh:         make(chan struct{}, 1),
		closeAckTimeout:    DefaultCloseAckTimeout,
		appendDataTimeout:  DefaultAppendDataTimeout,
		readTimeout:        DefaultReadTimeout,
		waitDataAckTimeout: DefaultWaitDataAckTimeout,
	}
}

func NewDialStream(w packet.Writer,
	localDomain string, localIP, localPort uint16,
	remoteDomain string, remoteIP, remotePort uint16) *Stream {
	s := New(w)
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
	remoteDomain string, remoteIP, remotePort uint16) *Stream {
	s := New(w)
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

func directionStr(d int) string {
	if d == DIRECTION_LOCAL_TO_REMOTE {
		return "local-remote"
	}
	if d == DIRECTION_REMOTE_TO_LOCAL {
		return "remote-local"
	}
	return "invalid"
}
