package node

import (
	"errors"
	"sync"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

var (
	errStreamNotFound      = errors.New("stream not found")
	errConvertStreamFailed = errors.New("convert stream failed")
	ErrInvalidListener     = errors.New("invalid listener")
)

type DataHub struct {
	// host    *Node
	portm   *numsrc.Manager
	streams sync.Map // map[sid]*stream.Conn
}

func (hub *DataHub) Init(portm *numsrc.Manager) {
	hub.portm = portm
}

func (hub *DataHub) AttachStream(s *stream.Conn, sid uint64) error {
	_, loaded := hub.streams.LoadOrStore(sid, s)
	if loaded {
		// 已经存在还未释放的stream
		return ErrSidIsAttached
	}
	return nil
}

func (hub *DataHub) GetStreamBySID(sid uint64, getAndDelete bool) (*stream.Conn, error) {
	// 收到close，代表对端不会再发送数据，可以解除streams绑定
	var it interface{}
	var found bool

	if getAndDelete {
		it, found = hub.streams.LoadAndDelete(sid)
	} else {
		it, found = hub.streams.Load(sid)
	}
	if !found {
		return nil, errStreamNotFound
	}

	c, ok := it.(*stream.Conn)
	if !ok {
		return nil, errConvertStreamFailed
	}

	return c, nil
}

// 处理数据包
func (hub *DataHub) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	c, err := hub.GetStreamBySID(pbuf.SID(), false)
	if err != nil {
		return
	}

	c.AppendData(pbuf.Payload)
}

// 处理数据包已送达的消息
func (hub *DataHub) HandleCmdPushStreamDataAck(pbuf *packet.Buffer) {
	c, err := hub.GetStreamBySID(pbuf.SID(), false)
	if err != nil {
		return
	}

	c.IncreaseBucket(pbuf.ACKInfo())
}

func (hub *DataHub) HandleCmdCloseStream(pbuf *packet.Buffer) {
	s, err := hub.GetStreamBySID(pbuf.SID(), true)
	if err != nil {
		return
	}

	s.AppendEOF()
	s.CloseWrite(true)

	port, err := s.GetUsedPort()
	if err != nil {
		return
	}
	hub.portm.ReleaseNumberSrc(port)
}

func (hub *DataHub) HandleCmdCloseStreamAck(pbuf *packet.Buffer) {
	s, err := hub.GetStreamBySID(pbuf.SID(), true)
	if err != nil {
		return
	}

	s.AppendEOF()

	port, err := s.GetUsedPort()
	if err != nil {
		return
	}
	hub.portm.ReleaseNumberSrc(port)
}
