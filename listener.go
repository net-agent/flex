package flex

import "errors"

type Listener struct {
	chanStream chan *Stream
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
	if stream == nil {
		return errors.New("push nil stream")
	}
	listener.chanStream <- stream
	return nil
}
