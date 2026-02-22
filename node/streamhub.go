package node

import (
	"errors"
	"sync"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
)

var (
	errStreamNotFound    = errors.New("stream not found")
	errInvalidStreamType = errors.New("invalid stream type")

	maxClosedStates = 1024
)

type StreamHub struct {
	host         *Node
	portm        *idpool.Pool
	streams      sync.Map        // map[sid]*stream.Stream
	closedStates []*stream.State // 保存已经关闭的连接状态（环形缓冲区，最多保留 maxClosedStates 条）
	closedPos    int             // 环形缓冲区写入位置
	closedMut    sync.RWMutex
}

func (hub *StreamHub) init(host *Node, portm *idpool.Pool) {
	hub.host = host
	hub.portm = portm
}

func (hub *StreamHub) attachStream(s *stream.Stream, sid uint64) error {
	_, loaded := hub.streams.LoadOrStore(sid, s)
	if loaded {
		// 已经存在还未释放的stream
		return ErrSidIsAttached
	}
	return nil
}

func (hub *StreamHub) getStream(sid uint64) (*stream.Stream, error) {
	it, found := hub.streams.Load(sid)
	if !found {
		return nil, errStreamNotFound
	}
	c, ok := it.(*stream.Stream)
	if !ok {
		return nil, errInvalidStreamType
	}
	return c, nil
}

func (hub *StreamHub) detachStream(sid uint64) (*stream.Stream, error) {
	it, found := hub.streams.LoadAndDelete(sid)
	if !found {
		return nil, errStreamNotFound
	}
	c, ok := it.(*stream.Stream)
	if !ok {
		return nil, errInvalidStreamType
	}
	return c, nil
}

func (hub *StreamHub) closeAllStreams() {
	hub.streams.Range(func(key, value interface{}) bool {
		if s, ok := value.(*stream.Stream); ok {
			s.CloseRead()
			s.CloseWrite()
		}
		hub.streams.Delete(key)
		return true
	})
}

func (hub *StreamHub) GetStreamStates() []*stream.State {
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
func (hub *StreamHub) GetClosedStates(pos int) []*stream.State {
	hub.closedMut.RLock()
	defer hub.closedMut.RUnlock()

	total := len(hub.closedStates)
	if total == 0 || pos >= total {
		return nil
	}

	// 环形缓冲区：按写入顺序从旧到新排列
	result := make([]*stream.State, 0, total-pos)
	for i := pos; i < total; i++ {
		idx := (hub.closedPos + i) % total
		if hub.closedStates[idx] != nil {
			result = append(result, hub.closedStates[idx])
		}
	}
	return result
}

// 处理数据包
func (hub *StreamHub) handleCmdPushStreamData(pbuf *packet.Buffer) {
	c, err := hub.getStream(pbuf.SID())
	if err != nil {
		if hub.host != nil {
			hub.host.logger.Warn("push data: stream not found, possibly closed", "sid", pbuf.SID())
		}
		return
	}

	c.HandleCmdPushStreamData(pbuf)
}

// 处理数据包已送达的消息
func (hub *StreamHub) handleAckPushStreamData(pbuf *packet.Buffer) {
	c, err := hub.getStream(pbuf.SID())
	if err != nil {
		if hub.host != nil {
			hub.host.logger.Warn("push data ack: stream not found, possibly closed", "sid", pbuf.SID())
		}
		return
	}
	c.HandleAckPushStreamData(pbuf)
}

func (hub *StreamHub) handleCmdCloseStream(pbuf *packet.Buffer) {
	hub.closeStream(pbuf, func(s *stream.Stream) { s.HandleCmdCloseStream(pbuf) })
}

func (hub *StreamHub) handleAckCloseStream(pbuf *packet.Buffer) {
	hub.closeStream(pbuf, func(s *stream.Stream) { s.HandleAckCloseStream(pbuf) })
}

func (hub *StreamHub) closeStream(pbuf *packet.Buffer, handle func(*stream.Stream)) {
	s, err := hub.detachStream(pbuf.SID())
	if err != nil {
		return
	}

	handle(s)
	if err := hub.portm.Release(s.GetUsedPort()); err != nil {
		hub.host.logger.Warn("release port failed", "port", s.GetUsedPort(), "error", err)
	}

	hub.recordClosedState(s.GetState())
}

func (hub *StreamHub) recordClosedState(state *stream.State) {
	hub.closedMut.Lock()
	defer hub.closedMut.Unlock()

	if len(hub.closedStates) < maxClosedStates {
		hub.closedStates = append(hub.closedStates, state)
	} else {
		hub.closedStates[hub.closedPos] = state
	}
	hub.closedPos = (hub.closedPos + 1) % maxClosedStates
}
