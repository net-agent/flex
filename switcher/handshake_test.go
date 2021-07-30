package switcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/net-agent/flex/packet"
)

func TestMarshal(t *testing.T) {
	var req Request
	req.Domain = "hello world"
	req.Mac = "xixisisittj"
	req.IV = packet.GetIV()

	buf, err := json.Marshal(&req)
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Println(buf)

	var req2 Request
	err = json.Unmarshal(buf, &req2)
	if err != nil {
		t.Error(err)
		return
	}

	if req2.Domain != req.Domain {
		t.Error("domain not equal")
		return
	}
	if req2.Mac != req.Mac {
		t.Error("mac not equal")
		return
	}
	if !bytes.Equal(req2.IV[:], req.IV[:]) {
		t.Error("iv not equal")
		return
	}
}
