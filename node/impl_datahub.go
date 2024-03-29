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
	portm              *numsrc.Manager
	streams            sync.Map        // map[sid]*stream.Conn
	closedStreamStates []*stream.State // 保存已经关闭的连接状态
	closedMut          sync.RWMutex
}

func (hub *DataHub) Init(portm *numsrc.Manager) {
	hub.portm = portm
}

func (hub *DataHub) AttachStream(s *stream.Stream, sid uint64) error {
	_, loaded := hub.streams.LoadOrStore(sid, s)
	if loaded {
		// 已经存在还未释放的stream
		return ErrSidIsAttached
	}
	return nil
}

func (hub *DataHub) GetStreamBySID(sid uint64, getAndDelete bool) (*stream.Stream, error) {
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

	c, ok := it.(*stream.Stream)
	if !ok {
		return nil, errConvertStreamFailed
	}

	return c, nil
}

func (hub *DataHub) GetStreamStateList() []*stream.State {
	list := []*stream.State{}
	hub.streams.Range(func(key, value interface{}) bool {
		s, ok := value.(*stream.Stream)
		if ok {
			list = append(list, s.GetState())
		}
		return true
	})
	return list
}
func (hub *DataHub) GetClosedStreamStateList(pos int) []*stream.State {
	hub.closedMut.RLock()
	defer hub.closedMut.RUnlock()
	if pos >= len(hub.closedStreamStates) {
		return nil
	}
	list := make([]*stream.State, len(hub.closedStreamStates)-pos)
	copy(list, hub.closedStreamStates[pos:])
	return list
}

// 处理数据包
func (hub *DataHub) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	c, err := hub.GetStreamBySID(pbuf.SID(), false)
	if err != nil {
		return
	}

	c.HandleCmdPushStreamData(pbuf)
}

// 处理数据包已送达的消息
func (hub *DataHub) HandleAckPushStreamData(pbuf *packet.Buffer) {
	c, err := hub.GetStreamBySID(pbuf.SID(), false)
	if err != nil {
		return
	}
	c.HandleAckPushStreamData(pbuf)
}

func (hub *DataHub) HandleCmdCloseStream(pbuf *packet.Buffer) {
	s, err := hub.GetStreamBySID(pbuf.SID(), true)
	if err != nil {
		return
	}

	s.HandleCmdCloseStream(pbuf)
	hub.portm.ReleaseNumberSrc(s.GetUsedPort())

	hub.closedMut.Lock()
	hub.closedStreamStates = append(hub.closedStreamStates, s.GetState())
	hub.closedMut.Unlock()
}

func (hub *DataHub) HandleAckCloseStream(pbuf *packet.Buffer) {
	s, err := hub.GetStreamBySID(pbuf.SID(), true)
	if err != nil {
		return
	}

	s.HandleAckCloseStream(pbuf)
	hub.portm.ReleaseNumberSrc(s.GetUsedPort())

	hub.closedMut.Lock()
	hub.closedStreamStates = append(hub.closedStreamStates, s.GetState())
	hub.closedMut.Unlock()
}
