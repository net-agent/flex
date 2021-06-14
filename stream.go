package flex

import (
	"encoding/binary"
	"errors"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Stream struct {
	host     *Host
	isClient bool // 主动发起连接的一方

	localPort  uint16
	remotePort uint16

	chanOpenACK chan struct{}

	readPipe *bytesPipe

	writeLocker        sync.Mutex
	writeClosed        bool
	writePoolSize      int32
	writePoolIncEvents chan struct{}
	writenCount        int64
	writenACKCount     int64
}

func NewStream(host *Host, isClient bool) *Stream {
	return &Stream{
		host:               host,
		isClient:           isClient,
		chanOpenACK:        make(chan struct{}),
		readPipe:           NewBytesPipe(),
		writeClosed:        false,
		writePoolSize:      1024 * 64, // 16字节缓冲
		writePoolIncEvents: make(chan struct{}, 128),
	}
}

func (stream *Stream) Read(dist []byte) (int, error) {
	rn, err := stream.readPipe.Read(dist)
	go func(readedCount int) {
		for readedCount > 0 {
			if readedCount > 0xffff {
				stream.readed(0xffff)
				readedCount -= 0xffff
			} else {
				stream.readed(uint16(readedCount))
				readedCount = 0
			}
		}
	}(rn)
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
			case <-time.After(time.Second * 3):
				log.Printf("[local=%v] write pool dry pool=%v w=%v ack=%v\n",
					stream.localPort, stream.writePoolSize,
					stream.writenCount, stream.writenACKCount)
			}
		}

		sliceSize := int(atomic.LoadInt32(&stream.writePoolSize))
		if sliceSize > size {
			sliceSize = size
		}
		end = start + sliceSize
		if end > len(src) {
			end = len(src)
			sliceSize = end - start
		}
		err := stream.write(src[start:end])
		if err != nil {
			return start, err
		}

		start = end
	}

	return start, nil
}

func (stream *Stream) write(buf []byte) error {
	stream.writeLocker.Lock()
	defer stream.writeLocker.Unlock()
	if stream.writeClosed {
		return errors.New("write to closed stream")
	}

	// 先申请配额
	// 后根据实际写入成功或失败情况，归还配额（失败时归还）
	atomic.AddInt32(&stream.writePoolSize, -int32(len(buf)))
	stream.writenCount += int64(len(buf))

	err := stream.host.writePacket(
		CmdPushStreamData,
		stream.localPort, stream.remotePort,
		buf,
	)
	if err != nil {
		stream.increasePoolSize(uint16(len(buf)))
	}
	return err
}

func (stream *Stream) Close() error {
	return stream.close()
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
	atomic.AddInt64(&stream.writenACKCount, int64(size))
	n := atomic.AddInt32(&stream.writePoolSize, int32(size))
	if debug || n > 1024 {
		log.Printf("[local=%v] writePoolSize=%v\n", stream.localPort, n)
	}
	// if len(stream.writePoolIncEvents) <= 0 {
	stream.writePoolIncEvents <- struct{}{}
	// } else {
	// 	log.Println("ignore inc event")
	// }
}

// close 主动关闭的意思：告诉对端，我不会再发送任何数据
// 对端可以从host.streams中解除绑定
//
func (stream *Stream) close() error {
	stream.writeLocker.Lock()
	defer stream.writeLocker.Unlock()
	if stream.writeClosed {
		return errors.New("close a closed stream")
	}
	stream.writeClosed = true

	return stream.host.writePacket(CmdCloseStream, stream.localPort, stream.remotePort, nil)
}

func (stream *Stream) LocalAddr() net.Addr {
	return stream.host.conn.LocalAddr()
}

func (stream *Stream) RemoteAddr() net.Addr {
	return stream.host.conn.RemoteAddr()
}

func (stream *Stream) SetDeadline(t time.Time) error {
	return errors.New("not implement")
}

func (stream *Stream) SetReadDeadline(t time.Time) error {
	return stream.readPipe.SetReadDeadline(t)
}

func (stream *Stream) SetWriteDeadline(t time.Time) error {
	return errors.New("not implement")
}
