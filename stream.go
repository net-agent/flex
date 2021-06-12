package flex

import (
	"encoding/binary"
	"errors"
	"net"
	"time"
)

type Stream struct {
	net.Conn
	host *Host

	localPort  uint16
	remotePort uint16

	chanOpenACK chan struct{}

	readPipe *bytesPipe
}

func NewStream(host *Host) *Stream {
	return &Stream{
		host:        host,
		chanOpenACK: make(chan struct{}),
		readPipe:    NewBytesPipe(),
	}
}

func (stream *Stream) Read(dist []byte) (int, error) {
	return stream.readPipe.Read(dist)
}

func (stream *Stream) Write(src []byte) (int, error) {
	size := 1024 * 16
	start := 0
	end := 0
	for start < len(src) {
		end = start + size
		if end > len(src) {
			end = len(src)
		}
		err := stream.host.writePacket(CmdPushStreamData,
			stream.localPort, stream.remotePort, src[start:end])

		start = end

		if err != nil {
			return start, err
		}
	}

	return start, nil
}

func (stream *Stream) id() uint32 {
	var buf [4]byte
	binary.BigEndian.PutUint16(buf[0:2], stream.remotePort) // src
	binary.BigEndian.PutUint16(buf[2:4], stream.localPort)  // dist
	return binary.BigEndian.Uint32(buf[:])
}

// open 主动开启连接
func (stream *Stream) open() error {
	err := stream.host.writePacket(CmdOpenStream, stream.localPort, stream.remotePort, nil)
	if err != nil {
		return err
	}

	select {
	case <-stream.chanOpenACK:
		return nil
	case <-time.After(time.Second * 10):
		return errors.New("timeout")
	}
}

// opened 响应开启连接请求
func (stream *Stream) opened() error {
	err := stream.host.writePacket(CmdOpenStream|CmdACKFlag, stream.localPort, stream.remotePort, nil)
	return err
}
