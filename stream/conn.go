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

type OpenResp struct {
	ErrCode int
	ErrMsg  string
	DistIP  uint16
}

type Conn struct {
	isDialer             bool
	dialer               string
	local                addr
	localIP, localPort   uint16
	remote               addr
	remoteIP, remotePort uint16

	// for open
	openAck chan *packet.Buffer

	// for reader
	rclosed   bool
	bytesChan chan []byte
	currBuf   []byte

	// for writer
	wmut       sync.Mutex
	wclosed    bool
	pwriter    packet.Writer
	pushBuf    *packet.Buffer
	pushAckBuf *packet.Buffer
	bucketSz   int32
	bucketEv   chan struct{}

	// counter
	counter Counter
}

func (s *Conn) String() string { return fmt.Sprintf("<%v,%v>", s.local.String(), s.remote.String()) }
func (s *Conn) State() string  { return fmt.Sprintf("%v %v", s.String(), s.counter.String()) }

func New(isDialer bool) *Conn {
	return &Conn{
		isDialer:  isDialer,
		dialer:    "self",
		openAck:   make(chan *packet.Buffer, 1),
		bytesChan: make(chan []byte, DefaultBytesChanCap),
		bucketSz:  DefaultBucketSize,
		bucketEv:  make(chan struct{}, 16),
	}
}

func (s *Conn) Dialer() string {
	if s.isDialer {
		return "self"
	}
	return s.dialer
}

func (s *Conn) SetDialer(dialer string) error {
	if s.isDialer {
		return errors.New("conn is dialer, can't set new dialer info")
	}
	s.dialer = fmt.Sprintf("%v:%v", dialer, s.remotePort)
	return nil
}

func (s *Conn) Close() error {
	return s.CloseWrite(false)
}

func (s *Conn) WaitOpenResp() (*packet.Buffer, error) {
	select {
	case pbuf := <-s.openAck:
		msg := string(pbuf.Payload)
		if msg != "" {
			return nil, errors.New(msg)
		}
		return pbuf, nil

	case <-time.After(DefaultDialTimeout):
		return nil, errors.New("dial timeout")
	}
}

func (s *Conn) Opened(pbuf *packet.Buffer) {
	s.openAck <- pbuf
}
