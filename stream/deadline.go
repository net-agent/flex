package stream

import (
	"errors"
	"log"
	"time"
)

type DeadlineGuard struct {
	deadline time.Time
}

func (guard *DeadlineGuard) Set(t time.Time, callback func()) error {
	if t.Equal(time.Time{}) {
		guard.deadline = time.Time{}
		return nil
	}
	dur := time.Until(t)
	if dur <= 0 {
		return errors.New("deadline should in future")
	}
	guard.deadline = t
	if callback != nil {
		go func() {
			<-time.After(dur)
			if callback != nil && t.Equal(guard.deadline) && !t.Equal(time.Time{}) {
				callback()
			}
		}()
	}
	return nil
}

func (s *Conn) SetDeadline(t time.Time) error {
	s.SetReadDeadline(t)
	s.SetWriteDeadline(t)
	return nil
}

func (s *Conn) SetWriteDeadline(t time.Time) error {
	if s.wclosed {
		return errors.New("writer closed")
	}
	return s.wDeadlineGuard.Set(t, func() {
		log.Println("write close by timeout")
		s.CloseWrite(false)
	})
}

func (s *Conn) SetReadDeadline(t time.Time) error {
	if s.rclosed {
		return errors.New("reader closed")
	}
	return s.rDeadlineGuard.Set(t, func() {
		log.Println("read close by timeout")
		s.AppendEOF()
	})
}
