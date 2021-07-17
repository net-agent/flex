package packet

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestChanBlock(t *testing.T) {
	ch := make(chan int, 5)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-time.After(time.Millisecond * 10)
		for n := range ch {
			fmt.Printf("val from ch: %v\n", n)
		}
	}()

	for i := 0; i < 7; i++ {
		select {
		case ch <- i:
		default:
			fmt.Printf("discard n: %v\n", i)
		}
	}

	close(ch)
	wg.Wait()
}

func BenchmarkCPUID(b *testing.B) {
	var head Header
	for i := 0; i < b.N; i++ {
		head.CPUID()
	}
}
