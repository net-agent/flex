package stream

import (
	"fmt"
	"time"
)

type State struct {
	Index    int32
	IsClosed bool
	Created  time.Time
	Closed   time.Time

	Direction    int
	LocalDomain  string
	LocalAddr    Addr
	RemoteDomain string
	RemoteAddr   Addr

	WritedBufferCount  int32
	HandledBufferCount int32
	HandledDataSize    int64
	HandledDataAckSum  int64
	SendDataAckSum     int64

	ConnReadSize  int64
	ConnWriteSize int64
}

func (st *State) String() string {
	// 收到数据包的总大小 -> 被读取出去的数据总大小 -> 应答给对端ack的总和 -> 写出去的数据总大小 -> 对端应答ack的总和
	return fmt.Sprintf("HandledDataSize=%v ConnReadSize=%v SendDataAckSum=%v ConnWriteSize=%v HandledDataAckSum=%v",
		st.HandledDataSize, st.ConnReadSize, st.SendDataAckSum, st.ConnWriteSize, st.HandledDataAckSum,
	)
}

func (st *State) Local() string {
	if st.LocalDomain == "" {
		return st.LocalAddr.str
	}
	return fmt.Sprintf("%v:%v", st.LocalDomain, st.LocalAddr.Port)
}
func (st *State) Remote() string {
	if st.RemoteDomain == "" {
		return st.RemoteAddr.str
	}
	return fmt.Sprintf("%v:%v", st.RemoteDomain, st.RemoteAddr.Port)
}
