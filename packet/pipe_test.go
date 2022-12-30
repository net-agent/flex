package packet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPipe(t *testing.T) {
	pc1, pc2 := Pipe()
	assert.NotNil(t, pc1)
	assert.NotNil(t, pc2)

	pc3, pc4 := WsPipe()
	assert.NotNil(t, pc3)
	assert.NotNil(t, pc4)

	pc1.SetReadTimeout(0)
	pc2.SetReadTimeout(time.Second * 12)
	pc1.SetWriteTimeout(0)
	pc2.SetWriteTimeout(time.Second * 12)

	pc3.SetReadTimeout(0)
	pc4.SetReadTimeout(time.Second * 12)
	pc3.SetWriteTimeout(0)
	pc4.SetWriteTimeout(time.Second * 12)
}

func TestWsPipe(t *testing.T) {
	wp := &wsPiper{}
	wp.Init()
	assert.Equal(t, wp.Addr().String(), "")
	assert.Equal(t, wp.Addr().Network(), "")

	wp.Close()
}
