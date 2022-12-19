package multiplexer

import "net"

func (s *Stream) Addr() net.Addr  { return s }
func (s *Stream) Network() string { return "" }
func (s *Stream) String() string  { return "" }
