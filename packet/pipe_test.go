package packet

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPipe(t *testing.T) {
	pc1, pc2 := Pipe()
	assert.NotNil(t, pc1)
	assert.NotNil(t, pc2)

	pc3, pc4 := WsPipe()
	assert.NotNil(t, pc3)
	assert.NotNil(t, pc4)
}

func TestWsPipe(t *testing.T) {
	wp := &wsPiper{}
	wp.Init()
	assert.Equal(t, wp.Addr().String(), "")
	assert.Equal(t, wp.Addr().Network(), "")
	wp.Close()
}
