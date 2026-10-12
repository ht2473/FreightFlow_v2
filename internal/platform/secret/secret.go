// Package secret шифрует чувствительные значения в базе и выдаёт случайные токены.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
)

// Box шифрует данные AES-256-GCM. Формат шифротекста: nonce || ciphertext.
type Box struct {
	aead cipher.AEAD
}

// NewBox создаёт шифратор из ключа в hex (32 байта).
func NewBox(hexKey string) (*Box, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal шифрует plaintext.
func (b *Box) Seal(plaintext []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plaintext, nil)
}

// Open расшифровывает данные, зашифрованные Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("ciphertext too short")
	}
	out, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return out, nil
}

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Token возвращает случайный токен из n байт в кодировке base32 (без символов, требующих экранирования в URL).
func Token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return tokenEncoding.EncodeToString(b)
}

// Hash возвращает SHA-256 значения. Используется для хранения токенов: в базе нет исходных значений.
func Hash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
