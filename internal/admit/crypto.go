package admit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

const saltSize = 16

// deriveKey 使用 HMAC-SHA256 从 password 和 salt 派生 AES-256 密钥
func deriveKey(password string, salt []byte) []byte {
	h := hmac.New(sha256.New, []byte(password))
	h.Write(salt)
	return h.Sum(nil) // 32 bytes = AES-256
}

// encrypt 使用 AES-GCM 加密 plaintext，返回 salt + nonce + ciphertext
func encrypt(plaintext []byte, password string) ([]byte, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	block, err := aes.NewCipher(deriveKey(password, salt))
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// salt + nonce + ciphertext(含 GCM tag)
	out := make([]byte, 0, saltSize+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// DeriveObfuscateKey 使用 HMAC-SHA256 从 password 和双方 nonce 派生混淆密钥。
func DeriveObfuscateKey(password string, clientNonce, serverNonce []byte) []byte {
	h := hmac.New(sha256.New, []byte(password))
	h.Write(clientNonce)
	h.Write(serverNonce)
	return h.Sum(nil) // 32 bytes
}

// decrypt 解密 encrypt 产生的数据
func decrypt(data []byte, password string) ([]byte, error) {
	if len(data) < saltSize {
		return nil, fmt.Errorf("data too short for salt")
	}

	salt := data[:saltSize]
	rest := data[saltSize:]

	block, err := aes.NewCipher(deriveKey(password, salt))
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	if len(rest) < gcm.NonceSize() {
		return nil, fmt.Errorf("data too short for nonce")
	}

	nonce := rest[:gcm.NonceSize()]
	ciphertext := rest[gcm.NonceSize():]

	return gcm.Open(nil, nonce, ciphertext, nil)
}
