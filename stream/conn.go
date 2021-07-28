package stream

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/net-agent/flex/packet"
)

const (
	KB                  = 1024
	MB                  = 1024 * KB
	DefaultBucketSize   = 512 * KB
	DefaultSplitSize    = 16 * KB
	DefaultBytesChanCap = 2 * DefaultBucketSize / DefaultSplitSize
)

type OpenResp struct {
	ErrCode int
	ErrMsg  string
	DistIP  uint16
}

type Conn struct {
	isDialer             bool
	local                addr
	localIP, localPort   uint16
	remote               addr
	remoteIP, remotePort uint16

	// for open
	openAck chan *packet.Buffer

	// for reader
	rclosed      bool
	bytesChan    chan []byte
	currBuf      []byte
	readedCount  int64 // 成功被读走的数据总量
	appendCount  int64 // 从对端发送过来的总量
	readAckCount int64 // 回应给对端的ack总量

	// for writer
	wmut          sync.Mutex
	wclosed       bool
	pwriter       packet.Writer
	pushBuf       *packet.Buffer
	pushAckBuf    *packet.Buffer
	bucketSz      int32
	bucketEv      chan struct{}
	writedCount   int64 // 发送给对方的总量
	writeAckCount int64 // 对端确认收到的总量
}

func (s *Conn) String() string {
	return fmt.Sprintf("%v:%v->%v:%v", s.localIP, s.localPort, s.remoteIP, s.remotePort)
}

func (s *Conn) State() string {
	return fmt.Sprintf("%v append=%v readed=%v readack=%v writed=%v writeack=%v",
		s.String(), s.appendCount, s.readedCount, s.readAckCount, s.writedCount, s.writeAckCount)
}

func New(isDialer bool) *Conn {
	return &Conn{
		isDialer:  isDialer,
		openAck:   make(chan *packet.Buffer, 1),
		bytesChan: make(chan []byte, DefaultBytesChanCap),
		bucketSz:  DefaultBucketSize,
		bucketEv:  make(chan struct{}, 16),
	}
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

	case <-time.After(time.Second * 500):
		return nil, errors.New("dial timeout")
	}
}

func (s *Conn) Opened(pbuf *packet.Buffer) {
	s.openAck <- pbuf
}
