package admit

import (
	"encoding/json"
	"testing"
)

// TestResponseMarshal 如果此用例失败，需要修改Response.WriteTo的实现
func TestResponseMarshal(t *testing.T) {
	var resp Response
	_, err := json.Marshal(&resp)
	if err != nil {
		t.Error("response marshal failed")
		return
	}
}
