package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shabbybastard/knigojor/internal/config"
)

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{30 * time.Second, "< 1 мин"},
		{5 * time.Minute, "5 мин"},
		{2*time.Hour + 5*time.Minute, "2 ч 5 мин"},
		{26*time.Hour + 3*time.Minute, "1 д 2 ч 3 мин"},
	}
	for _, test := range tests {
		if got := formatUptime(test.duration); got != test.want {
			t.Fatalf("formatUptime(%v) = %q, want %q", test.duration, got, test.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		value uint64
		want  string
	}{
		{0, "0 B"},
		{1024, "1 KB"},
		{5 * 1024 * 1024, "5 MB"},
		{1536 * 1024 * 1024, "1.5 GB"},
	}
	for _, test := range tests {
		if got := formatBytes(test.value); got != test.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestReadDiskUsage(t *testing.T) {
	free, total, ok := readDiskUsage(t.TempDir())
	if !ok {
		t.Fatal("disk usage is unavailable")
	}
	if total == 0 || free > total {
		t.Fatalf("unexpected disk usage: free=%d total=%d", free, total)
	}
}

func TestStatusTextContainsSystemStats(t *testing.T) {
	app := &App{
		Settings: &config.Settings{
			BaseDir:      t.TempDir(),
			ContentBase:  "https://flisland.test",
			DownloadBase: "https://flibusta.test",
		},
		StartTime: time.Now().Add(-2*time.Hour - 5*time.Minute),
		Version:   "test",
	}
	text := app.StatusText(context.Background())
	for _, wanted := range []string{
		"Аптайм: 2 ч 5 мин",
		"Ошибок: 0",
		"CPU:",
		"RAM:",
		"Процесс:",
		"Диск:",
		"Каталог: https://flisland.test",
		"Книги: https://flibusta.test",
		"Версия: test",
	} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("status is missing %q:\n%s", wanted, text)
		}
	}
}
