package packet

import "time"

var (
	LOG_WRITE_BUFFER_HEADER = false
	LOG_READ_BUFFER_HEADER  = false

	DefaultReadDeadline  = time.Second * 30
	DefaultWriteDeadline = time.Second * 10
)
