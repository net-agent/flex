package node

import (
	"sync"
	"testing"
	"time"

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
