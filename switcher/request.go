package switcher

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type Request struct {
	Version   int
	Domain    string
	Mac       string
	Timestamp int64
	Sum       string
}

// GenSum sha256(domain + mac + timestamp + password)
func (req *Request) CalcSum(password string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("CalcSumStart,%v,%v,%v,%v,CalcSumEnd", req.Domain, req.Mac, password, req.Timestamp)))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (req *Request) Marshal() []byte {
	// Request的成员都是基本类型，不会出现Marshal失败的情况
	// 如果Marshal失败，返回空buf，也不影响
	buf, _ := json.Marshal(req)
	return buf
}
