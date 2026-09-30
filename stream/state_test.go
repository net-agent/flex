package stream

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStateMarshal(t *testing.T) {
	var st State
	buf, err := json.MarshalIndent(&st, "", "  ")
	assert.Nil(t, err)
	assert.NotEmpty(t, buf)
}

func TestStateMarshalFieldNames(t *testing.T) {
	st := State{
		Index:        7,
		Direction:    DirectionOutbound,
		BytesRead:    100,
		BytesWritten: 200,
	}
	st.LocalAddr.SetIPPort(10, 20)

	buf, err := json.Marshal(&st)
	assert.Nil(t, err)
	s := string(buf)
	assert.Contains(t, s, `"index":7`)
	assert.Contains(t, s, `"bytes_read":100`)
	assert.Contains(t, s, `"bytes_written":200`)
	assert.Contains(t, s, `"local_addr":{"ip":10,"port":20}`)
}

func TestStateMethods(t *testing.T) {
	s := New(nil, 0)
	s.setLocal(10, 20)
	s.setRemote(30, 40)

	t.Run("without domain", func(t *testing.T) {
		assert.Equal(t, "10:20", s.state.Local())
		assert.Equal(t, "30:40", s.state.Remote())
	})

	t.Run("with domain", func(t *testing.T) {
		s.state.LocalDomain = "local"
		s.state.RemoteDomain = "remote"
		assert.Equal(t, "local:20", s.state.Local())
		assert.Equal(t, "remote:40", s.state.Remote())
	})
}

func TestAddrStruct(t *testing.T) {
	a := &Addr{}
	network := "testnetwork"
	ip := uint16(1234)
	port := uint16(5678)

	a.SetNetwork(network)
	a.SetIPPort(ip, port)

	assert.Equal(t, network, a.Network())
	assert.Equal(t, ip, a.IP)
	assert.Equal(t, port, a.Port)
	assert.Equal(t, "1234:5678", a.String())
}
