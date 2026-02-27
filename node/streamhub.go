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

	// 当 stream 双向都关闭时自动从 hub 中移除，并回收资源。
	s.SetOnDetach(func() {
		// Guard against SID reuse: only remove if this SID still points to the same stream.
		if cur, ok := hub.streams.Load(sid); ok && cur == s {
			hub.streams.Delete(sid)
		}
		if hub.portm != nil {
			if port := s.GetBoundPort(); port > 0 {
				if err := hub.portm.Release(port); err != nil && hub.host != nil {
					hub.host.logger.Warn("release port failed", "port", port, "error", err)
				}
			}
		}
		hub.recordClosedState(s.GetState())
	})

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
			hub.host.logger.Warn("push data: stream not found, possibly closed", "sid", pbuf.SID(), "header", pbuf.HeaderString())
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
			hub.host.logger.Warn("push data ack: stream not found, possibly closed", "sid", pbuf.SIDStr())
		}
		return
	}
	c.HandleAckPushStreamData(pbuf)
}

func (hub *StreamHub) handleCmdCloseStream(pbuf *packet.Buffer) {
	c, err := hub.getStream(pbuf.SID())
	if err != nil {
		// Stream already detached — still reply CloseAck so remote doesn't hang
		if hub.host != nil {
			ack := packet.NewBufferWithCmd(packet.AckCloseStream)
			ack.SetSrc(pbuf.DistIP(), pbuf.DistPort())
			ack.SetDist(pbuf.SrcIP(), pbuf.SrcPort())
			hub.host.WriteBuffer(ack)
		}
		return
	}
	c.HandleCmdCloseStream(pbuf)
}

func (hub *StreamHub) handleAckCloseStream(pbuf *packet.Buffer) {
	c, err := hub.getStream(pbuf.SID())
	if err != nil {
		return
	}
	c.HandleAckCloseStream(pbuf)
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
