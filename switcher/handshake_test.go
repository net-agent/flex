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

func TestCalcSum(t *testing.T) {
	cases := []struct {
		req    Request
		pswd   string
		output string
	}{
		{Request{"test.com", "mac", 0, ""}, "", "Pf1OHs4vBuZ0RiCJvIdFpB99c0Gra64rH6vYi0fXZDk="},
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
