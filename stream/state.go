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

	Direction    Direction
	LocalDomain  string
	LocalAddr    Addr
	RemoteDomain string
	RemoteAddr   Addr

	SentBufferCount int32
	RecvBufferCount int32
	RecvDataSize    int64
	RecvAckTotal    int64
	SentAckTotal    int64

	BytesRead    int64
	BytesWritten int64
}

func (st *State) String() string {
	return fmt.Sprintf("RecvDataSize=%v BytesRead=%v SentAckTotal=%v BytesWritten=%v RecvAckTotal=%v",
		st.RecvDataSize, st.BytesRead, st.SentAckTotal, st.BytesWritten, st.RecvAckTotal,
	)
}

func (st *State) Local() string {
	if st.LocalDomain == "" {
		return st.LocalAddr.text
	}
	return fmt.Sprintf("%v:%v", st.LocalDomain, st.LocalAddr.Port)
}
func (st *State) Remote() string {
	if st.RemoteDomain == "" {
		return st.RemoteAddr.text
	}
	return fmt.Sprintf("%v:%v", st.RemoteDomain, st.RemoteAddr.Port)
}

type Addr struct {
	network string
	text    string
	IP      uint16
	Port    uint16
}

func (a *Addr) String() string  { return a.text }
func (a *Addr) Network() string { return a.network }

func (a *Addr) SetNetwork(name string) { a.network = name }
func (a *Addr) SetIPPort(ip, port uint16) {
	a.text = fmt.Sprintf("%v:%v", ip, port)
	a.IP = ip
	a.Port = port
}
