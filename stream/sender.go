package stream

import (
	"errors"
	"sync"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	ErrSendDataOversize = errors.New("send data oversize")
)

type Sender struct {
	packet.Writer

	dataPbuf     *packet.Buffer
	dataAckPbuf  *packet.Buffer
	closePbuf    *packet.Buffer
	closeAckPbuf *packet.Buffer

	dataAckMut sync.Mutex
}

func NewSender(w packet.Writer) *Sender {
	s := &Sender{}

	s.Writer = w
	s.dataPbuf = packet.NewBufferWithCmd(packet.CmdPushStreamData)
	s.dataAckPbuf = packet.NewBufferWithCmd(packet.CmdPushStreamData | packet.CmdACKFlag)
	s.closePbuf = packet.NewBufferWithCmd(packet.CmdCloseStream)
	s.closeAckPbuf = packet.NewBufferWithCmd(packet.CmdCloseStream | packet.CmdACKFlag)

	return s
}

func (s *Sender) SetSrc(ip, port uint16) {
	s.dataPbuf.SetSrc(ip, port)
	s.dataAckPbuf.SetSrc(ip, port)
	s.closePbuf.SetSrc(ip, port)
	s.closeAckPbuf.SetSrc(ip, port)
}

func (s *Sender) SetDist(ip, port uint16) {
	s.dataPbuf.SetDist(ip, port)
	s.dataAckPbuf.SetDist(ip, port)
	s.closePbuf.SetDist(ip, port)
	s.closeAckPbuf.SetDist(ip, port)
}

func (s *Sender) SendCmdData(buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	if len(buf) > vars.MaxPayloadSize {
		return ErrSendDataOversize
	}

	s.dataPbuf.SetPayload(buf)
	return s.WriteBuffer(s.dataPbuf)
}

// SendCmdDataAck 回复dataAck，调用频率很高，且需要保证线程安全
func (s *Sender) SendCmdDataAck(n uint16) error {
	s.dataAckMut.Lock()
	defer s.dataAckMut.Unlock()
	s.dataAckPbuf.SetACKInfo(n)
	return s.WriteBuffer(s.dataAckPbuf)
}

func (s *Sender) SendCmdClose() error {
	return s.WriteBuffer(s.closePbuf)
}

func (s *Sender) SendCmdCloseAck() error {
	return s.WriteBuffer(s.closeAckPbuf)
}
