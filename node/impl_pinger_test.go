package node

import (
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPingDomain(t *testing.T) {
	node1, _ := Pipe("test1", "test2")
	var err error

	_, err = node1.PingDomain("test2", time.Second)
	assert.Nil(t, err, "test PingDomain")

	_, err = node1.PingDomain("notexists", time.Second)
	assert.NotNil(t, err, "test PingDomain error")
	log.Println(err)
}
