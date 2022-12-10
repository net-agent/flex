package node

import (
	"log"
	"testing"
	"time"
)

func TestPingDomain(t *testing.T) {
	node1, _ := Pipe("test1", "test2")
	var err error

	_, err = node1.PingDomain("test2", time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = node1.PingDomain("notexists", time.Second)
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	log.Println(err)
}
