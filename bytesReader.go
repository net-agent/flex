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
	locker       sync.RWMutex
	bufs         [][]byte
	pos          int
	appendEvents chan struct{}
	closedEvent  chan struct{}
	eofCount     int32
}

func NewBytesPipe() *bytesPipe {
	return &bytesPipe{
		bufs:         make([][]byte, 0),
		appendEvents: make(chan struct{}, 64),
		closedEvent:  make(chan struct{}, 1),
	}
}

func (pipe *bytesPipe) Read(dist []byte) (int, error) {
fillTopToDist:
	pipe.locker.RLock()
	if len(pipe.bufs) > 0 {
		top := pipe.bufs[0]
		start := pipe.pos
		end := start + len(dist)
		if end > len(top) {
			end = len(top)
		}
		readed := copy(dist, top[start:end])
		if end >= len(top) {
			pipe.bufs = pipe.bufs[1:]
			pipe.pos = 0
		} else {
			pipe.pos = end
		}
		pipe.locker.RUnlock()
		return readed, nil
	}
	pipe.locker.RUnlock()

	select {
	case <-pipe.closedEvent:
		if atomic.AddInt32(&pipe.eofCount, 1) == 1 {
			return 0, io.EOF
		}
		// 仅当closedEvent被close时，此分支才会被触发
		return 0, errors.New("read from closed bytesPipe")

	case <-pipe.appendEvents:
		goto fillTopToDist

	case <-time.After(time.Second * 3000):
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
