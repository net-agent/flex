package flex

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type bytesPipe struct {
	closed       bool
	locker       sync.Mutex
	bufs         [][]byte
	pos          int
	appendEvents chan struct{}
	closedEvent  chan struct{}
	readTimeout  time.Duration
	eofCount     int32
	readedCount  uint64
}

func NewBytesPipe() *bytesPipe {
	return &bytesPipe{
		bufs:         make([][]byte, 0),
		appendEvents: make(chan struct{}, 64),
		closedEvent:  make(chan struct{}, 1),
		readTimeout:  time.Second * 15,
	}
}

func (pipe *bytesPipe) Read(dist []byte) (retReaded int, retErr error) {
	defer func() {
		atomic.AddUint64(&pipe.readedCount, uint64(retReaded))
	}()
fillTopToDist:
	pipe.locker.Lock()
	if len(pipe.bufs) > 0 {
		// top: 最前面的缓冲区
		// start: 缓冲区可读数据起始
		// end: 缓冲区此次可读数据末尾
		top := pipe.bufs[0]
		start := pipe.pos
		end := start + len(dist)

		// 调整缓冲队列游标
		if end >= len(top) {
			end = len(top)
			pipe.bufs = pipe.bufs[1:]
			pipe.pos = 0
		} else {
			pipe.pos = end
		}

		readed := copy(dist, top[start:end])

		pipe.locker.Unlock()
		return readed, nil
	}
	pipe.locker.Unlock()

	if len(pipe.appendEvents) > 0 {
		<-pipe.appendEvents
		goto fillTopToDist
	}

	select {
	case <-pipe.appendEvents:
		goto fillTopToDist

	case <-pipe.closedEvent:
		if atomic.AddInt32(&pipe.eofCount, 1) == 1 {
			return 0, io.EOF
		}
		// 仅当closedEvent被close时，此分支才会被触发
		return 0, errors.New("read from closed bytesPipe")

	case <-time.After(pipe.readTimeout):
		return 0, errors.New("read timeout")
	}
}

func (pipe *bytesPipe) append(data []byte) error {
	if data == nil || len(data) <= 0 {
		return errors.New("empty data")
	}
	pipe.locker.Lock()
	if pipe.closed {
		pipe.locker.Unlock()
		return errors.New("append to closed bytesPipe")
	}
	pipe.bufs = append(pipe.bufs, data)
	pipe.locker.Unlock()

	if len(pipe.appendEvents) <= 0 {
		pipe.appendEvents <- struct{}{}
	}
	return nil
}

func (pipe *bytesPipe) Close() error {
	pipe.locker.Lock()
	defer pipe.locker.Unlock()
	if pipe.closed {
		return errors.New("closed")
	}

	pipe.closed = true
	close(pipe.closedEvent)

	return nil
}

func (pipe *bytesPipe) SetReadDeadline(t time.Time) error {
	return errors.New("not implement")
}
