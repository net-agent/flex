package link

import (
	"crypto/sha256"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

const (
	// handshakeVersion is the v4 protocol version. It is independent of the
	// packet.VERSION used by the old admit handshake.
	handshakeVersion byte = 4

	bootSalt      = "flex-boot-v4"
	bootKeyLen    = 32
	linkKeyLen    = 32
	handshakeSize = 512
)

// KDF selects how the network PSK is turned into the bootstrap key.
type KDF uint8

const (
	// KDFArgon2id is the default for human-chosen passwords. The bootstrap
	// key is derived once per process and cached by Dialer/Acceptor.
	KDFArgon2id KDF = iota
	// KDFHKDF is for high-entropy, machine-generated network secrets.
	KDFHKDF
)

var (
	errHandshakeRejected = errors.New("link: handshake rejected")
	errHandshakeVersion  = errors.New("link: handshake version mismatch")
	errHandshakeReplay   = errors.New("link: handshake replay")
)

func deriveBootKey(psk string, kdf KDF) []byte {
	switch kdf {
	case KDFHKDF:
		r := hkdf.New(sha256.New, []byte(psk), []byte(bootSalt), []byte("flex-boot-key"))
		out := make([]byte, bootKeyLen)
		if _, err := io.ReadFull(r, out); err != nil {
			panic(err)
		}
		return out
	default:
		return argon2.IDKey([]byte(psk), []byte(bootSalt), 3, 64*1024, 4, bootKeyLen)
	}
}

func hkdfKey(secret []byte, info string, salt []byte) []byte {
	r := hkdf.New(sha256.New, secret, salt, []byte(info))
	out := make([]byte, linkKeyLen)
	if _, err := io.ReadFull(r, out); err != nil {
		panic(err)
	}
	return out
}
