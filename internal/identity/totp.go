package identity

import (
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"freightflow/internal/platform/secret"
)

const totpIssuer = "FreightFlow"

// TOTPSetup — данные для подключения приложения-аутентификатора.
type TOTPSetup struct {
	Secret string // для ручного ввода
	URL    string // otpauth:// для QR-кода
}

func generateTOTP(email string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: email,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// validateTOTP проверяет код с допуском ±1 интервал (30 секунд) на расхождение часов.
func validateTOTP(code, secretValue string, now time.Time) bool {
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secretValue, now, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// newRecoveryCodes создаёт резервные коды вида XXXXX-XXXXX.
func newRecoveryCodes(n int) []string {
	codes := make([]string, n)
	for i := range codes {
		t := secret.Token(7)[:10]
		codes[i] = t[:5] + "-" + t[5:]
	}
	return codes
}

func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, " ", "")
	if len(code) == 10 && !strings.Contains(code, "-") {
		code = code[:5] + "-" + code[5:]
	}
	return code
}
