package flex

import (
	"net"
	"sync"
	"testing"
)

func TestSwitcher(t *testing.T) {
	c1, c2 := net.Pipe()
	var wg sync.WaitGroup

	// server
	wg.Add(1)
	go func() {
		defer wg.Done()
		switcher := NewSwitcher(nil)
		host, err := switcher.UpgradeHost(c2)
		if err != nil {
			t.Error(err)
			return
		}
		if host == nil {
			t.Error("not equal")
			return
		}
	}()

	// client
	wg.Add(1)
	go func() {
		defer wg.Done()

		host, err := UpgradeToHost(c1, &HostRequest{"test", "mac"})
		if err != nil {
			t.Error(err)
			return
		}
		if host == nil {
			t.Error("not equal")
			return
		}
	}()

	wg.Wait()
}
