package packet

import "testing"

func TestCipherConn(t *testing.T) {
	password := "helloworld"
	iv := GetIV()
	pc1, pc2 := Pipe()

	pc1, err := UpgradeCipher(pc1, password, iv)
	if err != nil {
		t.Error(err)
		return
	}

	pc2, err = UpgradeCipher(pc2, password, iv)
	if err != nil {
		t.Error(err)
		return
	}

	payload := NewBuffer(nil)
	payload.SetSrc(1, 2)
	payload.SetDist(3, 4)
	payload.SetPayload([]byte("hello world"))

	go pc1.WriteBuffer(payload)

	pbuf, err := pc2.ReadBuffer()
	if err != nil {
		t.Error(err)
		return
	}

	if pbuf.SrcIP() != 1 || pbuf.SrcPort() != 2 {
		t.Error(err)
		return
	}
}

// func TestXor(t *testing.T) {
// 	var iv1 [SizeOfIV]byte
// 	var iv2 [SizeOfIV]byte

// 	for i := 0; i < len(iv2); i++ {
// 		iv2[i] = 1
// 	}
// }
