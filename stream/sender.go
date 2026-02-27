package stream

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/net-agent/flex/v3/packet"
)

var (
	ErrSendDataOversize = errors.New("send data oversize")
)

type sender struct {
	packet.Writer

	dataBuf     *packet.Buffer
	dataAckBuf  *packet.Buffer
	closeBuf    *packet.Buffer
	closeAckBuf *packet.Buffer

	dataMu sync.Mutex
	ackMu  sync.Mutex

	counter *int32
}

func newSender(w packet.Writer, counter *int32) *sender {
	s := &sender{}

	s.Writer = w
	s.dataBuf = packet.NewBufferWithCmd(packet.CmdPushStreamData)
	s.dataAckBuf = packet.NewBufferWithCmd(packet.AckPushStreamData)
	s.closeBuf = packet.NewBufferWithCmd(packet.CmdCloseStream)
	s.closeAckBuf = packet.NewBufferWithCmd(packet.AckCloseStream)
	s.counter = counter

	return s
}

func (s *sender) SetSrc(ip, port uint16) {
	s.dataBuf.SetSrc(ip, port)
	s.dataAckBuf.SetSrc(ip, port)
	s.closeBuf.SetSrc(ip, port)
	s.closeAckBuf.SetSrc(ip, port)
}

func (s *sender) SetDst(ip, port uint16) {
	s.dataBuf.SetDist(ip, port)
	s.dataAckBuf.SetDist(ip, port)
	s.closeBuf.SetDist(ip, port)
	s.closeAckBuf.SetDist(ip, port)
}

func (s *sender) SendData(buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	if len(buf) > packet.MaxPayloadSize {
		return ErrSendDataOversize
	}

	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	if err := s.dataBuf.SetPayload(buf); err != nil {
		return err
	}
	atomic.AddInt32(s.counter, 1)
	return s.WriteBuffer(s.dataBuf)
}

// SendDataAck 回复dataAck，调用频率很高，且需要保证线程安全
func (s *sender) SendDataAck(n uint16) error {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()
	s.dataAckBuf.SetDataACKSize(n)
	atomic.AddInt32(s.counter, 1)
	return s.WriteBuffer(s.dataAckBuf)
}

func (s *sender) SendClose() error {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	atomic.AddInt32(s.counter, 1)
	return s.WriteBuffer(s.closeBuf)
}

func (s *sender) SendCloseAck() error {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()
	atomic.AddInt32(s.counter, 1)
	return s.WriteBuffer(s.closeAckBuf)
}
