package node

import (
	"time"
)

// lastWriteNano 返回最近一次写活跃时间（unix nano），供观测快照使用。
func (h *Heartbeat) lastWriteNano() int64 {
	return h.lastWriteTime.Load()
}

// setLastRTT 记录最近一次探活 RTT。
func (h *Heartbeat) setLastRTT(rtt time.Duration) {
	h.lastRTTNano.Store(int64(rtt))
}

// lastRTT 返回最近一次探活 RTT，未测得时为 0。
func (h *Heartbeat) lastRTT() time.Duration {
	return time.Duration(h.lastRTTNano.Load())
}
