package stream

import (
	"errors"
	"sync/atomic"
)

type Conn struct {
	bytesChan chan []byte
	currBuf   []byte

	openAck  chan []byte
	bucketSz int32
}

func New() *Conn {
	return &Conn{}
}

func (s *Conn) AppendData(buf []byte) {
	s.bytesChan <- buf
}

func (s *Conn) Read(dist []byte) (int, error) {
	if len(s.currBuf) == 0 {
		s.currBuf = <-s.bytesChan
	}

	n := copy(dist, s.currBuf)
	s.currBuf = s.currBuf[n:]
	return n, nil
}

func (s *Conn) Open() error {
	payload := <-s.openAck
	if len(payload) > 0 {
		return errors.New(string(payload))
	}
	return nil
}

func (s *Conn) Opened(payload []byte) {
	s.openAck <- payload
}

func (s *Conn) Close() error {
	return nil
}

func (s *Conn) IncreaseBucket(size uint16) {
	atomic.AddInt32(&s.bucketSz, int32(size))
	// todo: notify
}

func (s *Conn) AppendEOF() {}

func (s *Conn) StopWrite() {}
