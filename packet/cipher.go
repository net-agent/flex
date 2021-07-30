package packet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"io"
	"math/rand"
	"time"

	"golang.org/x/crypto/hkdf"
)

const SizeOfIV int = 16

type IV [SizeOfIV]byte

func init() {
	rand.Seed(time.Now().UnixNano())
}

func GetIV() IV {
	iv := IV{}
	rand.Read(iv[:])
	return iv
}

func Xor(dst, src *IV) {
	for i := 0; i < SizeOfIV; i++ {
		dst[i] = dst[i] ^ src[i]
	}
}

type cipherconn struct {
	Conn
	encoder cipher.Stream
	decoder cipher.Stream
}

func UpgradeCipher(c Conn, password string, iv IV) (Conn, error) {
	encoder, err := newCipherStream(password, iv)
	if err != nil {
		return nil, err
	}
	decoder, err := newCipherStream(password, iv)
	if err != nil {
		return nil, err
	}

	return &cipherconn{
		Conn:    c,
		encoder: encoder,
		decoder: decoder,
	}, nil
}

func (c *cipherconn) WriteBuffer(pbuf *Buffer) error {
	// c.encoder.XORKeyStream(pbuf.Head[:], pbuf.Head[:])
	c.encoder.XORKeyStream(pbuf.Payload, pbuf.Payload)
	return c.Conn.WriteBuffer(pbuf)
}

func (c *cipherconn) ReadBuffer() (*Buffer, error) {
	pbuf, err := c.Conn.ReadBuffer()
	if err != nil {
		return nil, err
	}
	// c.decoder.XORKeyStream(pbuf.Head[:], pbuf.Head[:])
	c.decoder.XORKeyStream(pbuf.Payload, pbuf.Payload)
	return pbuf, nil
}

func newCipherStream(password string, iv IV) (cipher.Stream, error) {
	key := make([]byte, SizeOfIV)
	err := hkdfSha1([]byte(password), key)
	if err != nil {
		return nil, err
	}
	bc, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewCTR(bc, iv[:]), nil
}

func hkdfSha1(secret, outbuf []byte) error {
	r := hkdf.New(sha1.New, secret, []byte("cipherconn-of-exchanger"), nil)
	if _, err := io.ReadFull(r, outbuf); err != nil {
		return err
	}
	return nil
}
