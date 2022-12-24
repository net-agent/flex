package stream

import (
	"fmt"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
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

	DIRECTION_LOCAL_TO_REMOTE = int(1)
	DIRECTION_REMOTE_TO_LOCAL = int(2)
)

type Stream struct {
	*Sender
	local        addr
	remote       addr
	localDomain  string
	remoteDomain string
	direction    int    // 从本地主动发出去的还是被动接受的
	usedPort     uint16 // 占用的端口。应当在Dial时进行绑定

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

	// counter
	counter Counter

	// variables
	appendDataTimeout  time.Duration
	readTimeout        time.Duration
	waitDataAckTimeout time.Duration
}

func (s *Stream) String() string {
	return fmt.Sprintf("[%v][%v,%v]", directionStr(s.direction), s.local.String(), s.remote.String())
}
func (s *Stream) State() string { return fmt.Sprintf("%v %v", s.String(), s.counter.String()) }

func New(pwriter packet.Writer) *Stream {
	return &Stream{
		Sender:             NewSender(pwriter),
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
	s.direction = DIRECTION_LOCAL_TO_REMOTE
	s.localDomain = localDomain
	s.remoteDomain = remoteDomain
	return s
}

func NewAcceptStream(w packet.Writer,
	localDomain string, localIP, localPort uint16,
	remoteDomain string, remoteIP, remotePort uint16) *Stream {
	s := New(w)
	s.SetLocal(localIP, localPort)
	s.SetRemote(remoteIP, remotePort)
	s.direction = DIRECTION_REMOTE_TO_LOCAL
	s.localDomain = localDomain
	s.remoteDomain = remoteDomain
	return s
}

func (s *Stream) GetReadWriteSize() (int64, int64) {
	return s.counter.ConnReadSize, s.counter.ConnWriteSize
}

func (s *Stream) SetUsedPort(port uint16)       { s.usedPort = port }
func (s *Stream) GetUsedPort() uint16           { return s.usedPort }
func (s *Stream) SetRemoteDomain(domain string) { s.remoteDomain = domain }

func directionStr(d int) string {
	if d == DIRECTION_LOCAL_TO_REMOTE {
		return "local-remote"
	}
	if d == DIRECTION_REMOTE_TO_LOCAL {
		return "remote-local"
	}
	return "invalid"
}
