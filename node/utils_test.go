package node

import (
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestNodePipe(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	var err error

	_, err = n1.PingDomain("test2", time.Second)
	assert.Nil(t, err, "test ping domain")

	_, err = n2.PingDomain("test1", time.Second)
	assert.Nil(t, err, "test ping domain")

	// 并发调用
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var testerr error

			_, testerr = n2.PingDomain("test1", time.Second)
			assert.Nil(t, testerr, "test ping domain")

			_, testerr = n1.PingDomain("test2", time.Second)
			assert.Nil(t, testerr, "test ping domain")
		}()
	}
	wg.Wait()
}

func Pipe(domain1, domain2 string) (*Node, *Node) {
	c1, c2 := packet.Pipe()
	node1 := New(c1)
	node2 := New(c2)

	node1.SetDomain(domain1)
	node1.SetIP(1)
	go node1.Serve()

	node2.SetDomain(domain2)
	node2.SetIP(2)
	go node2.Serve()

	return node1, node2
}
