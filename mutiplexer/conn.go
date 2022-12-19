package multiplexer

import (
	"net"
	"time"
)

func (s *Stream) Read(buf []byte) (int, error)       { return 0, nil }
func (s *Stream) Write(buf []byte) (int, error)      { return 0, nil }
func (s *Stream) Close() error                       { return nil }
func (s *Stream) LocalAddr() net.Addr                { return s }
func (s *Stream) RemoteAddr() net.Addr               { return s }
func (s *Stream) SetDeadline(t time.Time) error      { return nil }
func (s *Stream) SetReadDeadline(t time.Time) error  { return nil }
func (s *Stream) SetWriteDeadline(t time.Time) error { return nil }
