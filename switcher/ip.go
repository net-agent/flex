package switcher

import (
	"errors"
	"time"
)

func getFreeIpCh(min, max uint16) chan uint16 {
	ch := make(chan uint16, max-min)
	for i := min; i < max; i++ {
		ch <- i
	}
	return ch
}

func (s *Server) GetIP(domain string) (uint16, error) {
	for {
		select {
		case ip, ok := <-s.freeIps:
			if !ok {
				return 0, errors.New("alloc ip failed")
			}
			_, loaded := s.nodeIps.Load(ip)
			if loaded {
				continue
			}
			return ip, nil
		case <-time.After(time.Millisecond * 50):
			return 0, errors.New("alloc ip timeout")
		}
	}
}

func (s *Server) FreeIP(ip uint16) {
	if _, loaded := s.nodeIps.Load(ip); loaded {
		return
	}
	s.freeIps <- ip
}
