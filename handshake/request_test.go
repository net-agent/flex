package handshake

import (
	"encoding/json"
	"log"
	"testing"

	"github.com/net-agent/flex/v2/packet"
)

// TestRequestMarshal 如果本用例失败，则说明Request.Marshal方法会出现问题，需要修复其实现
func TestRequestMarshal(t *testing.T) {
	var req Request
	req.Domain = "hello world"
	req.Mac = "xixisisittj"

	buf, err := json.Marshal(&req)
	if err != nil {
		t.Error(err)
		return
	}

	log.Println(buf)

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

func TestCalcSum(t *testing.T) {
	cases := []struct {
		req    Request
		pswd   string
		output string
	}{
		{Request{packet.VERSION, "test.com", "mac", 0, ""}, "", "Pf1OHs4vBuZ0RiCJvIdFpB99c0Gra64rH6vYi0fXZDk="},
	}

	for _, c := range cases {
		sum := c.req.CalcSum(c.pswd)
		sum2 := c.req.CalcSum(c.pswd + "**")
		if sum == sum2 {
			t.Error("unexpected sum")
			return
		}
		if sum != c.output {
			t.Errorf("not equal, sum='%v' expect='%v'", sum, c.output)
			return
		}
	}
}
