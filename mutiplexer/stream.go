package multiplexer

import (
	"net"

	"github.com/net-agent/flex/v2/packet"
)

type Stream struct {
	packet.Writer
}

func NewClientStream() *Stream { return nil }
func NewServerStream() *Stream { return nil }

func (s *Stream) AsNetConn() net.Conn { return s }

func (s *Stream) sendCmdOpen() error     { return nil }
func (s *Stream) sendCmdOpenAck() error  { return nil }
func (s *Stream) sendCmdData() error     { return nil }
func (s *Stream) sendCmdDataAck() error  { return nil }
func (s *Stream) sendCmdClose() error    { return nil }
func (s *Stream) sendCmdCloseAck() error { return nil }
