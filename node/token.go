package node

import (
	"math/rand"
	"sync/atomic"
	"time"
)

var tokenIndex uint64

func init() {
	rand.Seed(time.Now().UnixNano())
	tokenIndex = rand.Uint64()
}

func NextToken() byte {
	return byte(atomic.AddUint64(&tokenIndex, 4) & 0xFF)
}
