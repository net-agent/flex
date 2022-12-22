package stream

import "fmt"

type Counter struct {
	HandlePushDataSize   int64
	HandlePushDataAckSum int64
	SendDataAck          int64
	ConnReadSize         int64
	ConnWriteSize        int64
}

func (c *Counter) String() string {
	return fmt.Sprintf(
		"HandleData=%v ConnRead=%v DataAck=%v ConnWrite=%v HandleDataAck=%v",
		c.HandlePushDataSize, c.ConnReadSize, c.SendDataAck, c.ConnWriteSize, c.HandlePushDataAckSum,
	)
}
