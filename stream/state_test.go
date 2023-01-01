package stream

import (
	"encoding/json"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateMarshal(t *testing.T) {
	var st State

	buf, err := json.MarshalIndent(&st, "", "  ")
	assert.Nil(t, err)
	log.Println(string(buf))
}

func TestStateMethods(t *testing.T) {
	s := New(nil)

	s.SetLocal(10, 20)
	s.SetRemote(30, 40)
	assert.Equal(t, "10:20", s.state.Local())
	assert.Equal(t, "30:40", s.state.Remote())

	s.state.LocalDomain = "local"
	s.state.RemoteDomain = "remote"
	assert.Equal(t, "local:20", s.state.Local())
	assert.Equal(t, "remote:40", s.state.Remote())
}
