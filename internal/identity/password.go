package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
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
	case weakPassword(password):
		return &ValidationError{Field: "password", Message: "Пароль слишком простой: повтор одного символа или ряд клавиш или цифр"}
	}
	return nil
}

// sequences — ряды, из которых составляют простые пароли: цифры, латиница и кириллица по
// алфавиту и по клавиатуре.
var sequences = []string{
	"012345678901234567890", "abcdefghijklmnopqrstuvwxyz", "абвгдеёжзийклмнопрстуфхцчшщъыьэюя",
	"qwertyuiopasdfghjklzxcvbnm", "йцукенгшщзхъфывапролджэячсмитьбю", "1qaz2wsx3edc4rfv5tgb6yhn7ujm",
}

// weakPassword — пароль из одного повторяющегося символа или из отрезка (в любом направлении)
// одного ряда, возможно повторённого: «aaaaaaaaaaaa», «123456789012», «qwertyqwerty».
func weakPassword(password string) bool {
	p := []rune(strings.ToLower(password))
	if len(p) == 0 {
		return false
	}
	// Повтор короткого фрагмента — проверяется сам фрагмент.
	for k := 1; k <= len(p)/2; k++ {
		periodic := true
		for i := k; i < len(p) && periodic; i++ {
			periodic = p[i] == p[i%k]
		}
		if periodic {
			p = p[:k]
			break
		}
	}
	same := true
	for _, r := range p[1:] {
		same = same && r == p[0]
	}
	if same {
		return true
	}
	for _, seq := range sequences {
		s := []rune(seq)
		rev := slices.Clone(s)
		slices.Reverse(rev)
		for _, row := range [][]rune{s, rev} {
			if onRow(p, row) {
				return true
			}
		}
	}
	return false
}

// onRow — каждый следующий символ пароля стоит в ряду сразу за предыдущим (по кругу).
func onRow(p, row []rune) bool {
	pos := slices.Index(row, p[0])
	if pos < 0 {
		return false
	}
	for _, r := range p[1:] {
		pos = (pos + 1) % len(row)
		if row[pos] != r {
			return false
		}
	}
	return true
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
