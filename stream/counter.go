package stream

import "fmt"

type Counter struct {
	AppendData     int64
	Read           int64
	WriteAck       int64
	Write          int64
	IncreaseBucket int64
}

func (c *Counter) String() string {
	return fmt.Sprintf(
		"AppendData=%v Read=%v WriteAck=%v Write=%v IncreaseBucket=%v",
		c.AppendData, c.Read, c.WriteAck, c.Write, c.IncreaseBucket,
	)
}
