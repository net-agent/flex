package node

import (
	"errors"
	"sync"
	"time"

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
	streamSidMap       sync.Map        // map[sid]*stream.Conn
	closedStreamStates []*stream.State // 保存已经关闭的连接状态
	closedMut          sync.RWMutex

	resetSidMap     sync.Map
	closeOnce       sync.Once
	stopJanitorChan chan struct{}
}

func (hub *DataHub) Init(portm *numsrc.Manager) {
	hub.portm = portm

	go hub.resetSidJanitor()
}

func (hub *DataHub) Close() {
	hub.closeOnce.Do(func() {
		close(hub.stopJanitorChan)
	})
}

func (hub *DataHub) AttachStream(s *stream.Stream, sid uint64) error {
	_, found := hub.resetSidMap.Load(sid)
	if found {
		return ErrSidIsResetState
	}

	_, loaded := hub.streamSidMap.LoadOrStore(sid, s)
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
		it, found = hub.streamSidMap.LoadAndDelete(sid)
	} else {
		it, found = hub.streamSidMap.Load(sid)
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
	hub.streamSidMap.Range(func(key, value interface{}) bool {
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
	sid := pbuf.SID()
	c, err := hub.GetStreamBySID(sid, false)
	if err != nil {
		return
	}

	err = c.HandleCmdPushStreamData(pbuf)

	if err != nil {
		c.PopupWarning("push data failed.", err.Error())

		if err == stream.ErrPushToByteChanTimeout {
			hub.streamSidMap.Delete(sid) // 清理掉这个stream，未来不再接收任何新的包
			hub.resetSidMap.Store(sid, time.Now())
			go c.SendCmdReset()

			// 在2分钟后将resetSidMap的记录去掉，即reset状态只有2分钟
			// 这个动作由resetJanitor来定期执行
		}
	}
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
