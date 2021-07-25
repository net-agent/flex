package stream

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/packet"
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
}

func New(isDialer bool) *Conn {
	return &Conn{
		isDialer:  isDialer,
		openAck:   make(chan *packet.Buffer, 1),
		bytesChan: make(chan []byte, 1024),
		bucketSz:  1024 * 1024 * 1,
		bucketEv:  make(chan struct{}, 16),
	}
}

func (s *Conn) GetUsedPort() (uint16, error) {
	if s.isDialer {
		return s.localPort, nil
	}
	return s.localPort, errors.New("local port still on listen")
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