package flex

import (
	"errors"
	"sync"
)

type Listener struct {
	chanLocker sync.Mutex
	chanStream chan *Stream
	closed     bool
}

func NewListener() *Listener {
	return &Listener{
		chanStream: make(chan *Stream, 128),
	}
}

func (listener *Listener) Accept() (*Stream, error) {
	stream, ok := <-listener.chanStream
	if !ok {
		return nil, errors.New("listener closed")
	}
	return stream, nil
}

func (listener *Listener) pushStream(stream *Stream) error {
	listener.chanLocker.Lock()
	defer listener.chanLocker.Unlock()

	if stream == nil {
		return errors.New("push nil stream")
	}
	if listener.closed {
		return errors.New("listener closed")
	}
	listener.chanStream <- stream
	return nil
}

func (listener *Listener) Close() error {
	listener.chanLocker.Lock()
	defer listener.chanLocker.Unlock()

	if listener.closed {
		return errors.New("close closed listener")
	}
	listener.closed = true
	close(listener.chanStream)

	return nil
}
