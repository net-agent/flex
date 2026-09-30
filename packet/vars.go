package packet

import "time"

var (
	LogWriteBufferHeader = false
	LogReadBufferHeader  = false

	DefaultReadTimeout  = time.Second * 30
	DefaultWriteTimeout = time.Second * 10
)
