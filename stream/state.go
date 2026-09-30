package stream

import (
	"fmt"
	"time"
)

type State struct {
	Index    int32     `json:"index"`
	IsClosed bool      `json:"is_closed"`
	Created  time.Time `json:"created"`
	Closed   time.Time `json:"closed"`

	Direction    Direction `json:"direction"` // 1=outbound(local→remote)，2=inbound(remote→local)
	LocalDomain  string    `json:"local_domain"`
	LocalAddr    Addr      `json:"local_addr"`
	RemoteDomain string    `json:"remote_domain"`
	RemoteAddr   Addr      `json:"remote_addr"`

	SentBufferCount int32 `json:"sent_buffer_count"`
	RecvBufferCount int32 `json:"recv_buffer_count"`
	RecvDataSize    int64 `json:"recv_data_size"`
	RecvAckTotal    int64 `json:"recv_ack_total"`
	SentAckTotal    int64 `json:"sent_ack_total"`

	BytesRead    int64 `json:"bytes_read"`
	BytesWritten int64 `json:"bytes_written"`
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
	IP      uint16 `json:"ip"`
	Port    uint16 `json:"port"`
}

func (a *Addr) String() string  { return a.text }
func (a *Addr) Network() string { return a.network }

func (a *Addr) SetNetwork(name string) { a.network = name }
func (a *Addr) SetIPPort(ip, port uint16) {
	a.text = fmt.Sprintf("%v:%v", ip, port)
	a.IP = ip
	a.Port = port
}
