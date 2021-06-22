package flex

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const defaultPoolBufferSize = 1024 * 1024 // 1MB的流控池大小
const defaultMaxTransUnitSize = 1024 * 32 // 32KB的最大传输单元（最大64KB）

type Stream struct {
	host     *Host
	isClient bool // 主动发起连接的一方。在关闭时需要归还可用端口

	localIP    HostIP
	remoteIP   HostIP
	localPort  uint16
	remotePort uint16
	dataID     uint64
	desc       string
	dialer     string // 创建连接的Host的Domain信息

	chanOpenACK  chan struct{}
	chanCloseACK chan struct{}

	readPipe *bytesPipe

	writeLocker        sync.Mutex
	writeClosed        bool
	writeCloseEvents   chan struct{}
	writePoolSize      int32
	writePoolIncEvents chan struct{}
	writenCount        int64
	writenACKCount     int64
	maxTransUnitSize   int

	onceClose sync.Once
}

func NewStream(host *Host, isClient bool) *Stream {
	var ip HostIP = 0
	if host != nil {
		ip = host.ip
	}
	return &Stream{
		host:               host,
		localIP:            ip,
		isClient:           isClient,
		dialer:             "self",
		chanOpenACK:        make(chan struct{}, 10),
		chanCloseACK:       make(chan struct{}, 10),
		readPipe:           NewBytesPipe(),
		writeClosed:        false,
		writeCloseEvents:   make(chan struct{}),
		writePoolSize:      defaultPoolBufferSize,
		writePoolIncEvents: make(chan struct{}, 128),
		maxTransUnitSize:   defaultMaxTransUnitSize,
	}
}

func (stream *Stream) SetAddr(localIP HostIP, localPort uint16, remoteIP HostIP, remotePort uint16) {
	stream.localIP = localIP
	stream.localPort = localPort
	stream.remoteIP = remoteIP
	stream.remotePort = remotePort

	var buf [8]byte
	binary.BigEndian.PutUint16(buf[0:2], stream.remoteIP)   // src-ip
	binary.BigEndian.PutUint16(buf[2:4], stream.localIP)    // dist-ip
	binary.BigEndian.PutUint16(buf[4:6], stream.remotePort) // src-port
	binary.BigEndian.PutUint16(buf[6:8], stream.localPort)  // dist-port
	stream.dataID = binary.BigEndian.Uint64(buf[:])
	stream.desc = fmt.Sprintf("%v:%v - %v:%v", localIP, localPort, remoteIP, remotePort)
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

// Write 把大块数据按照MTU值进行拆分，发送至对端。尽力而为发送所有数据
func (stream *Stream) Write(src []byte) (int, error) {
	start := 0
	end := 0

	for start < len(src) {

		// 判断对端是否还有足够的空间接收数据
		// 如果发出去的数据迟迟没有收到ACK，则代表对端的数据消化能力较低，需要暂停发送

		for atomic.LoadInt32(&stream.writePoolSize) <= 0 {
			if stream.writeClosed {
				return start, errors.New("stream closed")
			}
			select {
			case <-stream.writeCloseEvents:
				return start, errors.New("stream closed")
			case <-stream.writePoolIncEvents:
			case <-time.After(time.Second * 15):
				return start, fmt.Errorf("%v remote bytes pool overflow", stream.desc)
			}
		}

		size := int(atomic.LoadInt32(&stream.writePoolSize))
		if size > stream.maxTransUnitSize {
			size = stream.maxTransUnitSize
		}

		end = start + size
		if end > len(src) {
			end = len(src)
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
	if stream.writeClosed {
		return errors.New("write to closed stream")
	}

	stream.writeLocker.Lock()
	defer stream.writeLocker.Unlock()

	// double check
	if stream.writeClosed {
		return errors.New("write to closed stream")
	}

	// 先申请配额
	// 后根据实际写入成功或失败情况，归还配额（失败时归还）
	atomic.AddInt32(&stream.writePoolSize, -int32(len(buf)))

	err := stream.writePacket(CmdPushStreamData, 0, buf)
	if err != nil {
		stream.increasePoolSize(uint16(len(buf)))
	}

	stream.writenCount += int64(len(buf))

	return err
}

func (stream *Stream) writePacket(cmd byte, ackInfo uint16, payload []byte) error {
	return stream.host.writePacket(cmd,
		stream.localIP, stream.remoteIP,
		stream.localPort, stream.remotePort,
		ackInfo, payload,
	)
}

func (stream *Stream) Close() error {
	var err error
	stream.onceClose.Do(func() {
		err = stream.close()
	})
	return err
}

// open 主动开启连接
func (stream *Stream) open(domain string) error {
	cmd := CmdOpenStream
	payload := []byte(domain)

	err := stream.writePacket(cmd, 0, payload)

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
	stream.writePacket(CmdACKFlag|CmdOpenStream, 0, nil)
}

// readed 成功读取数据后，返回ACK
func (stream *Stream) readed(size uint16) {
	stream.writePacket(CmdACKFlag|CmdPushStreamData, size, nil)
}

func (stream *Stream) increasePoolSize(size uint16) {
	atomic.AddInt64(&stream.writenACKCount, int64(size))
	atomic.AddInt32(&stream.writePoolSize, int32(size))

	// memory leak here
	select {
	case stream.writePoolIncEvents <- struct{}{}:
	default:
	}

}

// close 关闭的意思：停止发送数据，并且告诉对端，我不会再发送任何数据
// - 对端可以安全执行：host.detach(stream) <此操作在onCloseStream中调用>
// - 对端可以安全执行：stream.readPipe.Close() <此操作在closed中调用>
//
func (stream *Stream) close() error {
	stream.writeLocker.Lock()
	if stream.writeClosed {
		stream.writeLocker.Unlock()
		return nil
	}
	stream.writeClosed = true // 设置为true后，将不会再有数据写到对端
	close(stream.writeCloseEvents)
	err := stream.writePacket(CmdCloseStream, 0, nil) // CmdCloseStream相当于EOF
	stream.writeLocker.Unlock()

	if err != nil {
		return err
	}

	select {

	case <-stream.chanCloseACK:
		// 收到对端的ACK，可以确定对端不会再发送任何数据
		// - 可以安全执行：host.detach(stream) <此操作在onCloseStreamACK中>
		// - 可以安全执行：stream.readPipe.Close()
		return stream.readPipe.Close()

	case <-time.After(time.Second * 10):
		return errors.New("wait close ack timeout")
	}
}

func (stream *Stream) closed() {
	// 关闭读取管道，收到对端的close指令后，不会再有新数据过来
	stream.readPipe.Close()

	stream.writeLocker.Lock()
	if stream.writeClosed {
		stream.writeLocker.Unlock()
		return
	}
	stream.writeClosed = true
	select {
	case <-stream.writeCloseEvents:
	default:
		close(stream.writeCloseEvents)
	}
	stream.writePacket(CmdACKFlag|CmdCloseStream, 0, nil)
	stream.writeLocker.Unlock()
}

func (stream *Stream) LocalAddr() net.Addr {
	return stream.host.LocalAddr()
}

func (stream *Stream) RemoteAddr() net.Addr {
	return stream.host.RemoteAddr()
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
