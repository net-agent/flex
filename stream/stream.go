package stream

import (
	"errors"
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

	DefaultDialTimeout       = time.Second * 5  // 此参数设置应该考虑网络延时，延时大的情况下需设置大一些
	DefaultAppendDataTimeout = time.Second * 2  // 此参数设置过小会导致丢包。过大会导致全局阻塞
	DefaultReadTimeout       = time.Minute * 10 // 此参数设置过小会导致长连接容易断开
)

type Stream struct {
	*Sender
	isDialer             bool
	dialer               string
	local                addr
	localIP, localPort   uint16
	remote               addr
	remoteIP, remotePort uint16

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

	// counter
	counter Counter
}

func (s *Stream) String() string { return fmt.Sprintf("<%v,%v>", s.local.String(), s.remote.String()) }
func (s *Stream) State() string  { return fmt.Sprintf("%v %v", s.String(), s.counter.String()) }

func New(pwriter packet.Writer, isDialer bool) *Stream {
	return &Stream{
		Sender:         NewSender(pwriter),
		isDialer:       isDialer,
		dialer:         "self",
		bytesChan:      make(chan []byte, DefaultBytesChanCap),
		bucketSz:       DefaultBucketSize,
		bucketEv:       make(chan struct{}, 16),
		rDeadlineGuard: &DeadlineGuard{},
		wDeadlineGuard: &DeadlineGuard{},
	}
}

func (s *Stream) Dialer() string {
	if s.isDialer {
		return "self"
	}
	return s.dialer
}

func (s *Stream) SetDialer(dialer string) error {
	if s.isDialer {
		return errors.New("conn is dialer, can't set new dialer info")
	}
	s.dialer = dialer
	return nil
}
