package multiplexer

import "github.com/net-agent/flex/v2/packet"

func (s *Stream) HandleCmdOpen(pbuf *packet.Buffer)     {}
func (s *Stream) HandleCmdOpenAck(pbuf *packet.Buffer)  {}
func (s *Stream) HandleCmdData(pbuf *packet.Buffer)     {}
func (s *Stream) HandleCmdDataAck(pbuf *packet.Buffer)  {}
func (s *Stream) HandleCmdClose(pbuf *packet.Buffer)    {}
func (s *Stream) HandleCmdCloseAck(pbuf *packet.Buffer) {}
