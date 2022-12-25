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
