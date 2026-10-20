package node

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
)

const (
	// DefaultDetachRetention 是流完全关闭后的默认滞留时长：滞留期内流仍可按 SID
	// 寻址，晚到的包由 stream 层逻辑消化。可用 Node.SetDetachRetention 按节点覆盖。
	DefaultDetachRetention = 2 * time.Second
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
	closedSeq    int64           // 累计写入 closedStates 的记录数（单调递增，作为增量拉取的游标）
	closedMut    sync.RWMutex

	// detachRetention 仅在 Serve 前写入（init/SetDetachRetention），运行期只读
	detachRetention time.Duration
}

func (hub *StreamHub) init(host *Node, portm *idpool.Pool) {
	hub.host = host
	hub.portm = portm
	hub.detachRetention = DefaultDetachRetention
}

func (hub *StreamHub) attachStream(s *stream.Stream, sid uint64) error {
	_, loaded := hub.streams.LoadOrStore(sid, s)
	if loaded {
		// 已经存在还未释放的stream
		return ErrSidIsAttached
	}

	// 当 stream 双向都关闭时自动从 hub 中移除，并回收资源。
	s.SetOnDetach(func() {
		hub.scheduleStreamCleanup(sid, s)
	})

	hub.host.traceStreamOpen(s.GetState())

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

func (hub *StreamHub) scheduleStreamCleanup(sid uint64, s *stream.Stream) {
	doCleanup := func() {
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
	}
	retention := hub.detachRetention
	if retention <= 0 {
		doCleanup()
		return
	}
	time.AfterFunc(retention, doCleanup)
}

func (hub *StreamHub) closeAllStreams() {
	hub.streams.Range(func(key, value interface{}) bool {
		if s, ok := value.(*stream.Stream); ok {
			// 节点关闭统一预设原因（first-wins，不被 CloseRead/CloseWrite 的 "local" 覆盖）
			s.SetCloseReason("node")
			s.CloseRead()
			s.CloseWrite()
		}
		hub.streams.Delete(key)
		return true
	})
}

// closeStreamByIndex 按 State.Index 查找活跃流并关闭（手动关闭，CloseReason 记为 "manual"）。
// 已完全关闭但仍在滞留期内的流视为未找到，返回 false。
func (hub *StreamHub) closeStreamByIndex(index int32) bool {
	var target *stream.Stream
	hub.streams.Range(func(key, value interface{}) bool {
		s, ok := value.(*stream.Stream)
		if !ok {
			return true
		}
		st := s.GetState()
		if st.Index == index && !st.IsClosed {
			target = s
			return false
		}
		return true
	})
	if target == nil {
		return false
	}
	target.SetCloseReason("manual")
	target.CloseRead()
	target.CloseWrite()
	return true
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

// GetClosedStates 增量拉取已关闭流的状态。
// pos 为上次返回的 nextPos（首次传 0）；返回 (新增状态列表, 新的 nextPos)。
// 缓冲区写满后旧记录被覆盖：若 pos 落后于最早可用记录，从最早可用处开始返回，
// 调用方比较 pos 与实际起点可感知缺口。
func (hub *StreamHub) GetClosedStates(pos int64) ([]*stream.State, int64) {
	hub.closedMut.RLock()
	defer hub.closedMut.RUnlock()

	seq := hub.closedSeq
	n := int64(len(hub.closedStates))
	if n == 0 || pos >= seq {
		return nil, seq
	}

	start := pos
	if start < seq-n {
		start = seq - n // pos 指向的记录已被覆盖，从最早可用记录开始
	}

	// 环形缓冲区：第 i 条记录（按写入序号）位于 closedStates[i%n]
	result := make([]*stream.State, 0, seq-start)
	for i := start; i < seq; i++ {
		if st := hub.closedStates[i%n]; st != nil {
			result = append(result, st)
		}
	}
	return result, seq
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
	hub.host.traceStreamClosed(state)

	hub.closedMut.Lock()
	defer hub.closedMut.Unlock()

	if len(hub.closedStates) < maxClosedStates {
		hub.closedStates = append(hub.closedStates, state)
	} else {
		hub.closedStates[hub.closedSeq%int64(maxClosedStates)] = state
	}
	hub.closedSeq++
}
