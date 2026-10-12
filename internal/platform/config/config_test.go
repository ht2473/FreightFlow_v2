package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotenvDoesNotOverrideEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# комментарий\nFF_TEST_A=from-file\nFF_TEST_B=\"quoted value\"\n\nFF_TEST_C=x=y\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FF_TEST_A", "from-env")
	// Переменные, которых нет в окружении, регистрируем через Setenv, чтобы тест их очистил.
	t.Setenv("FF_TEST_B", "")
	t.Setenv("FF_TEST_C", "")
	os.Unsetenv("FF_TEST_B")
	os.Unsetenv("FF_TEST_C")

	if err := loadDotenv(path); err != nil {
		t.Fatalf("loadDotenv: %v", err)
	}
	cases := map[string]string{
		"FF_TEST_A": "from-env",
		"FF_TEST_B": "quoted value",
		"FF_TEST_C": "x=y",
	}
	for key, want := range cases {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadDotenvMissingFile(t *testing.T) {
	if err := loadDotenv(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("missing file must be ignored, got %v", err)
	}
}

func TestLoadDotenvInvalidLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NOT_A_PAIR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDotenv(path); err == nil {
		t.Fatal("expected error for line without '='")
	}
}

func TestValidateRequiresKeysInProd(t *testing.T) {
	cfg := Config{Env: "prod", SessionKey: "short", EncryptionKey: "short"}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for short keys in prod")
	}
	cfg.Env = "staging"
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for unknown environment")
	}
	cfg = Config{Env: "dev"}
	if err := cfg.validate(); err != nil {
		t.Fatalf("dev config must be valid without keys: %v", err)
	}
}
