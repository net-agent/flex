package packet

import (
	"crypto/sha256"
	"sync"

	"golang.org/x/crypto/chacha20"
)

const obfuscateSize = 9 // Head[0:9] = Cmd + DistIP + DistPort + SrcIP + SrcPort

// ObfuscateHeader 原地 XOR header 的前 9 字节（跳过 PayloadSize/ACKSize）。
func ObfuscateHeader(head *Header, mask *[obfuscateSize]byte) {
	for i := range mask {
		head[i] ^= mask[i]
	}
}

// ObfuscateState 持有 per-connection 的混淆状态。
// ChaCha20 流密码内部维护 counter，天然保证每次输出不同的 keystream。
type ObfuscateState struct {
	writeCipher *chacha20.Cipher
	readCipher  *chacha20.Cipher
	writeMu     sync.Mutex
}

// NewObfuscateState 创建混淆状态。
// 用 SHA-256(key) 派生 32 字节 ChaCha20 密钥，nonce 固定为零
// （同一连接内 cipher 实例唯一，counter 单调递增，无 nonce 重用风险）。
func NewObfuscateState(key []byte) *ObfuscateState {
	derived := sha256.Sum256(key)
	var nonce [chacha20.NonceSize]byte

	wc, _ := chacha20.NewUnauthenticatedCipher(derived[:], nonce[:])
	rc, _ := chacha20.NewUnauthenticatedCipher(derived[:], nonce[:])

	return &ObfuscateState{writeCipher: wc, readCipher: rc}
}

// maskForWrite 从 write cipher 取 9 字节 keystream。调用方必须持有 writeMu。
func (s *ObfuscateState) maskForWrite() [obfuscateSize]byte {
	var mask [obfuscateSize]byte
	s.writeCipher.XORKeyStream(mask[:], mask[:])
	return mask
}

// MaskForRead 从 read cipher 取 9 字节 keystream。单 reader 语义，无需加锁。
func (s *ObfuscateState) MaskForRead() [obfuscateSize]byte {
	var mask [obfuscateSize]byte
	s.readCipher.XORKeyStream(mask[:], mask[:])
	return mask
}
