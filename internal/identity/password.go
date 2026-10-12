package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Параметры argon2id: память 64 МБ, 2 прохода, 2 потока — около 100–250 мс на сервере.
const (
	argonMemory  uint32 = 64 * 1024
	argonTime    uint32 = 2
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16

	minPasswordLen = 12
	maxPasswordLen = 128
)

var errMalformedHash = errors.New("malformed password hash")

// ValidatePassword проверяет требования к паролю.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < minPasswordLen:
		return &ValidationError{Field: "password", Message: fmt.Sprintf("Пароль должен содержать не менее %d символов", minPasswordLen)}
	case n > maxPasswordLen:
		return &ValidationError{Field: "password", Message: fmt.Sprintf("Пароль должен содержать не более %d символов", maxPasswordLen)}
	}
	return nil
}

// HashPassword возвращает хеш в формате PHC: $argon2id$v=19$m=…,t=…,p=…$соль$хеш.
func HashPassword(password string) string {
	salt := make([]byte, argonSaltLen)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// VerifyPassword сравнивает пароль с хешем за постоянное время. needsRehash сообщает, что хеш
// создан с устаревшими параметрами и его стоит пересчитать.
func VerifyPassword(password, encoded string) (ok, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, errMalformedHash
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, false, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, false, errMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want))) //nolint:gosec // длина ключа ограничена форматом
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	stale := memory != argonMemory || iterations != argonTime || threads != argonThreads || uint32(len(want)) != argonKeyLen //nolint:gosec // см. выше
	return true, stale, nil
}
