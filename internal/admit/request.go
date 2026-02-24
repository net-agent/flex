package admit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/net-agent/flex/v3/packet"
)

type Request struct {
	Version   int
	Domain    string
	Mac       string
	Timestamp int64
	Sum       string
	Nonce     []byte
}

func (req *Request) CalcSum(password string) string {
	h := hmac.New(sha256.New, []byte(password))
	fmt.Fprintf(h, "%v,%v,%v", req.Domain, req.Mac, req.Timestamp)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (req *Request) Marshal() ([]byte, error) {
	return json.Marshal(req)
}

func (req *Request) WriteTo(pc packet.Conn, password string) error {
	if len(req.Nonce) == 0 {
		req.Nonce = make([]byte, 16)
		if _, err := rand.Read(req.Nonce); err != nil {
			return err
		}
	}
	plaintext, err := req.Marshal()
	if err != nil {
		return err
	}
	ciphertext, err := encrypt(plaintext, password)
	if err != nil {
		return err
	}
	pbuf := packet.NewBufferWithCmd(packet.CmdAdmit)
	randomFillHeader(pbuf)
	if err := pbuf.SetPayload(ciphertext); err != nil {
		return err
	}
	return pc.WriteBuffer(pbuf)
}

func (req *Request) ReadFrom(pc packet.Conn, password string) error {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}
	plaintext, err := decrypt(pbuf.Payload, password)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, req)
}

// randomFillHeader 对 Header[1:9] 填充随机字节（DistIP/DistPort/SrcIP/SrcPort）
func randomFillHeader(pbuf *packet.Buffer) {
	rand.Read(pbuf.Head[1:9])
}
