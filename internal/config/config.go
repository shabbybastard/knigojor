// Package config loads and validates knigojor settings from the environment
// and from ~/.knigojor/.env.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Settings is the complete runtime configuration.
type Settings struct {
	BotToken      string
	AllowedUserID int64
	LogLevel      string

	BaseDir      string
	DatabasePath string
	TempPath     string
	TorStateDir  string

	TorEnabled     bool
	TorExecutable  string
	TorSocksHost   string
	TorSocksPort   int
	TorControlPort int

	HTTPTimeout         int
	DownloadTimeout     int
	HealthcheckInterval int

	MaxRetries           int
	MaxParallelRequests  int
	MaxParallelDownloads int

	Mirrors      []string
	OnionMirrors []string

	MaxFileSizeMB int
	PageSize      int
}

// Home returns the current user's home directory.
func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return home, nil
}

// BaseDir returns ~/.knigojor.
func BaseDir() string {
	home, err := Home()
	if err != nil {
		return ".knigojor"
	}
	return filepath.Join(home, ".knigojor")
}

// Load reads ~/.knigojor/.env and the process environment.
func Load(envFile string) (*Settings, error) {
	return LoadFrom(BaseDir(), envFile)
}

// LoadFrom is Load with an explicit base directory. It is used by tests and
// by deployments that want a different state directory.
func LoadFrom(base, envFile string) (*Settings, error) {
	if envFile == "" {
		envFile = filepath.Join(base, ".env")
	}
	if err := loadDotEnv(envFile); err != nil {
		return nil, err
	}

	botToken := strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if botToken == "" {
		return nil, errors.New("BOT_TOKEN is required (set in ~/.knigojor/.env or env)")
	}
	rawUserID := strings.TrimSpace(os.Getenv("ALLOWED_USER_ID"))
	if rawUserID == "" {
		return nil, errors.New("ALLOWED_USER_ID is required (set in ~/.knigojor/.env or env)")
	}
	allowedUserID, err := parseInt64("ALLOWED_USER_ID", rawUserID)
	if err != nil {
		return nil, err
	}

	mirrors := make([]string, 0, 10)
	onion := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		if value := strings.TrimSpace(os.Getenv(fmt.Sprintf("MIRROR_%d", i))); value != "" {
			parsed, err := parseURL(value, fmt.Sprintf("MIRROR_%d", i))
			if err != nil {
				return nil, err
			}
			mirrors = append(mirrors, parsed)
		}
		if value := strings.TrimSpace(os.Getenv(fmt.Sprintf("ONION_%d", i))); value != "" {
			parsed, err := parseURL(value, fmt.Sprintf("ONION_%d", i))
			if err != nil {
				return nil, err
			}
			onion = append(onion, parsed)
		}
	}
	if len(mirrors) == 0 {
		mirrors = []string{"https://flibusta.is", "https://flibusta.lib"}
	}

	torEnabled, err := parseBool("TOR_ENABLED", true)
	if err != nil {
		return nil, err
	}

	settings := &Settings{
		BotToken:      botToken,
		AllowedUserID: allowedUserID,
		LogLevel:      envOr("LOG_LEVEL", "INFO"),
		BaseDir:       base,
		TorEnabled:    torEnabled,
		TorExecutable: envOr("TOR_EXECUTABLE", "/usr/bin/tor"),
		TorSocksHost:  envOr("TOR_SOCKS_HOST", "127.0.0.1"),
		Mirrors:       mirrors,
		OnionMirrors:  onion,
	}

	if settings.DatabasePath, err = filepath.Abs(filepath.Join(base, "knigojor.db")); err != nil {
		return nil, err
	}
	if settings.TempPath, err = filepath.Abs(filepath.Join(base, "temp")); err != nil {
		return nil, err
	}
	if settings.TorStateDir, err = filepath.Abs(filepath.Join(base, "tor_state")); err != nil {
		return nil, err
	}

	ints := []struct {
		name     string
		env      string
		def      int
		min, max int
		dest     *int
	}{
		{"TOR_SOCKS_PORT", "TOR_SOCKS_PORT", 9050, 1, 65535, &settings.TorSocksPort},
		{"TOR_CONTROL_PORT", "TOR_CONTROL_PORT", 9051, 1, 65535, &settings.TorControlPort},
		{"HTTP_TIMEOUT", "HTTP_TIMEOUT", 15, 1, 600, &settings.HTTPTimeout},
		{"DOWNLOAD_TIMEOUT", "DOWNLOAD_TIMEOUT", 180, 1, 3600, &settings.DownloadTimeout},
		{"HEALTHCHECK_INTERVAL", "HEALTHCHECK_INTERVAL", 300, 30, 3600, &settings.HealthcheckInterval},
		{"MAX_RETRIES", "MAX_RETRIES", 3, 0, 10, &settings.MaxRetries},
		{"MAX_PARALLEL_REQUESTS", "MAX_PARALLEL_REQUESTS", 5, 1, 100, &settings.MaxParallelRequests},
		{"MAX_PARALLEL_DOWNLOADS", "MAX_PARALLEL_DOWNLOADS", 1, 1, 10, &settings.MaxParallelDownloads},
		{"MAX_FILE_SIZE_MB", "MAX_FILE_SIZE_MB", 50, 1, 50, &settings.MaxFileSizeMB},
		{"PAGE_SIZE", "PAGE_SIZE", 5, 1, 50, &settings.PageSize},
	}
	for _, item := range ints {
		value, err := parseEnvInt(item.name, item.def, item.min, item.max)
		if err != nil {
			return nil, err
		}
		*item.dest = value
	}

	if err := settings.prepareDirs(); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *Settings) prepareDirs() error {
	for _, dir := range []string{s.BaseDir, s.TempPath, s.TorStateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("chmod %s: %w", dir, err)
		}
	}
	return nil
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	_ = os.Chmod(path, 0o600)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("set %s from %s: %w", key, path, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseEnvInt(name string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	return parseInt(name, raw, min, max)
}

func parseInt64(name, raw string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", name, raw)
	}
	if value < 1 {
		return 0, fmt.Errorf("%s must be >= 1, got %d", name, value)
	}
	return value, nil
}

func parseInt(name, raw string, min, max int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", name, raw)
	}
	if min != 0 && value < min {
		return 0, fmt.Errorf("%s must be >= %d, got %d", name, min, value)
	}
	if max != 0 && value > max {
		return 0, fmt.Errorf("%s must be <= %d, got %d", name, max, value)
	}
	return value, nil
}

func parseBool(name string, def bool) (bool, error) {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if raw == "" {
		return def, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean, got %q", name, raw)
	}
}

func parseURL(value, name string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%s must be an http(s) URL: %w", name, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("%s must be an http(s) URL, got %q", name, value)
	}
	return strings.TrimRight(value, "/"), nil
}
