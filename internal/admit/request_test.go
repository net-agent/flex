package admit

import (
	"encoding/json"
	"testing"
)

func TestRequestMarshal(t *testing.T) {
	var req Request
	req.Domain = "hello world"
	req.Mac = "xixisisittj"

	buf, err := json.Marshal(&req)
	if err != nil {
		t.Error(err)
		return
	}

	var req2 Request
	err = json.Unmarshal(buf, &req2)
	if err != nil {
		t.Error(err)
		return
	}

	if req2.Domain != req.Domain {
		t.Error("domain not equal")
	}
	if req2.Mac != req.Mac {
		t.Error("mac not equal")
	}
}

func TestCalcSum(t *testing.T) {
	req := Request{Domain: "test.com", Mac: "mac", Timestamp: 0}

	// deterministic
	sum1 := req.CalcSum("pswd")
	sum2 := req.CalcSum("pswd")
	if sum1 != sum2 {
		t.Error("CalcSum not deterministic")
	}

	// different password → different sum
	sum3 := req.CalcSum("pswd**")
	if sum1 == sum3 {
		t.Error("different passwords should produce different sums")
	}
}
