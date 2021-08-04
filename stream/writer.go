package stream

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

func (s *Conn) InitWriter(w packet.Writer) {
	s.pwriter = w

	s.pushBuf = packet.NewBuffer(nil)
	s.pushBuf.SetCmd(packet.CmdPushStreamData)
	s.pushBuf.SetSrc(s.localIP, s.localPort)
	s.pushBuf.SetDist(s.remoteIP, s.remotePort)

	s.pushAckBuf = packet.NewBuffer(nil)
	s.pushAckBuf.SetCmd(packet.CmdPushStreamData | packet.CmdACKFlag)
	s.pushAckBuf.SetSrc(s.localIP, s.localPort)
	s.pushAckBuf.SetDist(s.remoteIP, s.remotePort)
}

// CloseWrite 设置写状态为不可写，并且告诉对端
func (s *Conn) CloseWrite(isACK bool) error {
	s.wmut.Lock()
	defer s.wmut.Unlock()

	if s.wclosed {
		return errors.New("close on closed conn")
	}

	s.wclosed = true

	// send close cmd to peer
	pbuf := packet.NewBuffer(nil)
	copy(pbuf.Head[:], s.pushBuf.Head[:])
	pbuf.SetCmd(packet.CmdCloseStream)
	if isACK {
		pbuf.SetCmd(packet.CmdCloseStream | packet.CmdACKFlag)
	}
	pbuf.SetPayload(nil)
	return s.pwriter.WriteBuffer(pbuf)
}

func (s *Conn) Write(buf []byte) (int, error) {
	var buflen = len(buf)
	var wn int
	const sz = DefaultSplitSize
	var slice []byte

	for {
		if len(buf) <= 0 {
			return wn, nil
		}
		if atomic.LoadInt32(&s.bucketSz) <= 0 {
			select {
			case <-s.bucketEv:
				continue
			case <-time.After(time.Second * 5):

				return wn, fmt.Errorf("bucket dry. writed=%v/%v state=%v", wn, buflen, s.State())
			}
		}

		// 确定本次传输的数据
		// MIN(sz, bucketSz, len(buf))
		maxSz := atomic.LoadInt32(&s.bucketSz)
		if sz < maxSz {
			maxSz = sz
		}
		if len(buf) > int(maxSz) {
			slice = buf[:maxSz]
		} else {
			slice = buf
		}

		n, err := s.write(slice)
		atomic.AddInt32(&s.bucketSz, -int32(n))
		wn += n
		if err != nil {
			return wn, err
		}
		buf = buf[n:]

		// 让其它stream有机会传输
		runtime.Gosched()
	}
}

// write 一次调用最大支持64KB传输
func (s *Conn) write(buf []byte) (int, error) {
	s.pushBuf.SetPayload(buf)

	s.wmut.Lock()
	defer s.wmut.Unlock()
	if s.wclosed {
		return 0, errors.New("write on closed conn")
	}

	err := s.pwriter.WriteBuffer(s.pushBuf)
	if err != nil {
		return 0, err
	}
	atomic.AddInt64(&s.counter.Write, int64(len(buf)))
	return len(buf), nil
}

func (s *Conn) writeACK(n uint16) {
	s.pushAckBuf.SetACKInfo(n)

	s.wmut.Lock()
	defer s.wmut.Unlock()
	if s.wclosed {
		return
	}

	err := s.pwriter.WriteBuffer(s.pushAckBuf)
	if err != nil {
		log.Println("write push-ack failed")
	}

	atomic.AddInt64(&s.counter.WriteAck, int64(n))
}

func (s *Conn) IncreaseBucket(size uint16) {
	atomic.AddInt32(&s.bucketSz, int32(size))
	atomic.AddInt64(&s.counter.IncreaseBucket, int64(size))

	select {
	case s.bucketEv <- struct{}{}:
	default:
	}
}
