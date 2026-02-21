package sched

import (
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// FairConn wraps a packet.Conn and uses FairWriter for writing
type FairConn struct {
	packet.Conn
	writer *FairWriter
}

func NewFairConn(conn packet.Conn) *FairConn {
	return &FairConn{
		Conn:   conn,
		writer: NewFairWriter(conn),
	}
}

func (fc *FairConn) WriteBuffer(buf *packet.Buffer) error {
	return fc.writer.WriteBuffer(buf)
}

func (fc *FairConn) SetWriteTimeout(dur time.Duration) {
	fc.writer.SetWriteTimeout(dur)
}

func (fc *FairConn) Close() error {
	_ = fc.writer.Close()
	return fc.Conn.Close()
}
