package switcher

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestMarshal(t *testing.T) {
	var req Request
	req.Domain = "hello world"
	req.Mac = "xixisisittj"

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
}
