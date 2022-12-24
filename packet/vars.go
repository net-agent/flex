package packet

import "time"

var (
	LOG_WRITE_BUFFER_HEADER = false
	LOG_READ_BUFFER_HEADER  = false

	DefaultReadTimeout  = time.Second * 30
	DefaultWriteTimeout = time.Second * 10
)
