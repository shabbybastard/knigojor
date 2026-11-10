package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom(t *testing.T) {
	base := t.TempDir()
	t.Setenv("BOT_TOKEN", "123:abc")
	t.Setenv("ALLOWED_USER_ID", "42")
	t.Setenv("PAGE_SIZE", "7")
	t.Setenv("MIRROR_1", "https://mirror.example/")
	t.Setenv("TOR_ENABLED", "false")

	settings, err := LoadFrom(base, filepath.Join(base, "missing.env"))
	if err != nil {
		t.Fatal(err)
	}
	if settings.BotToken != "123:abc" || settings.AllowedUserID != 42 {
		t.Fatalf("unexpected credentials: %#v", settings)
	}
	if settings.PageSize != 7 {
		t.Fatalf("unexpected page size: %d", settings.PageSize)
	}
	if len(settings.Mirrors) != 1 || settings.Mirrors[0] != "https://mirror.example" {
		t.Fatalf("unexpected mirrors: %#v", settings.Mirrors)
	}
	if settings.TorEnabled {
		t.Fatal("Tor should be disabled")
	}
	if settings.DatabasePath != filepath.Join(base, "knigojor.db") {
		t.Fatalf("unexpected database path: %s", settings.DatabasePath)
	}
	if _, err := os.Stat(filepath.Join(base, "temp")); err != nil {
		t.Fatalf("temp directory missing: %v", err)
	}
}

func TestLoadRequiresToken(t *testing.T) {
	base := t.TempDir()
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("ALLOWED_USER_ID", "1")
	if _, err := LoadFrom(base, filepath.Join(base, "missing.env")); err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestLoadRejectsInvalidPageSize(t *testing.T) {
	base := t.TempDir()
	t.Setenv("BOT_TOKEN", "123:abc")
	t.Setenv("ALLOWED_USER_ID", "1")
	t.Setenv("PAGE_SIZE", "999")
	if _, err := LoadFrom(base, filepath.Join(base, "missing.env")); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDotEnvDoesNotOverrideProcessEnv(t *testing.T) {
	base := t.TempDir()
	envFile := filepath.Join(base, ".env")
	if err := os.WriteFile(envFile, []byte("BOT_TOKEN=from-file\nALLOWED_USER_ID=7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_TOKEN", "from-env")
	t.Setenv("ALLOWED_USER_ID", "7")

	settings, err := LoadFrom(base, envFile)
	if err != nil {
		t.Fatal(err)
	}
	if settings.BotToken != "from-env" {
		t.Fatalf("dotenv overrode process env: %s", settings.BotToken)
	}
}
