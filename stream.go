package flex

import (
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Stream struct {
	net.Conn
	host     *Host
	isClient bool // 主动发起连接的一方

	localPort  uint16
	remotePort uint16

	chanOpenACK chan struct{}

	readPipe *bytesPipe

	writePoolSize      int32
	writePoolIncEvents chan struct{}
	writePoolLocker    sync.RWMutex
}

func NewStream(host *Host, isClient bool) *Stream {
	return &Stream{
		host:               host,
		isClient:           isClient,
		chanOpenACK:        make(chan struct{}),
		readPipe:           NewBytesPipe(),
		writePoolSize:      1024, // 16字节缓冲
		writePoolIncEvents: make(chan struct{}, 8),
	}
}

func (stream *Stream) Read(dist []byte) (int, error) {
	rn, err := stream.readPipe.Read(dist)
	go func() {
		for rn > 0 {
			if rn > 0xffff {
				stream.readed(0xffff)
				rn -= 0xffff
			} else {
				stream.readed(uint16(rn))
				rn = 0
			}
		}
	}()
	return rn, err
}

func (stream *Stream) Write(src []byte) (int, error) {
	size := 1024 * 16
	start := 0
	end := 0
	for start < len(src) {

		// 判断对端是否还有足够的空间接收数据
		// 如果发出去的数据迟迟没有收到ACK，则代表对端的数据消化能力较低，需要暂停发送

		for atomic.LoadInt32(&stream.writePoolSize) <= 0 {
			select {
			case <-stream.writePoolIncEvents:
			}
		}

		sliceSize := int(atomic.LoadInt32(&stream.writePoolSize))
		if sliceSize > size {
			sliceSize = size
		}
		end = start + sliceSize
		if end > len(src) {
			end = len(src)
			sliceSize = len(src) - start
		}
		err := stream.host.writePacket(CmdPushStreamData,
			stream.localPort, stream.remotePort, src[start:end])

		start = end
		atomic.AddInt32(&stream.writePoolSize, -int32(sliceSize))

		if err != nil {
			return start, err
		}
	}

	return start, nil
}

func (stream *Stream) dataID() uint32 {
	var buf [4]byte

	// 对端发送过来的数据包中：
	// head.src  = remote
	// head.dist = local
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
	case <-time.After(time.Second * 3):
		return errors.New("timeout")
	}
}

// opened 响应开启连接请求，返回ACK
func (stream *Stream) opened() {
	stream.host.writePacketACK(CmdOpenStream, stream.localPort, stream.remotePort, 0)
}

// readed 成功读取数据后，返回ACK
func (stream *Stream) readed(size uint16) {
	stream.host.writePacketACK(CmdPushStreamData, stream.localPort, stream.remotePort, size)
}

func (stream *Stream) increasePoolSize(size uint16) {
	atomic.AddInt32(&stream.writePoolSize, int32(size))
	stream.writePoolLocker.Lock()
	defer stream.writePoolLocker.Unlock()
	if len(stream.writePoolIncEvents) <= 0 {
		stream.writePoolIncEvents <- struct{}{}
	}
}
