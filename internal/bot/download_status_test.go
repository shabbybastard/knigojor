package bot

import (
	"strings"
	"testing"
)

func TestFormatDownloadProgress(t *testing.T) {
	const mb = 1024 * 1024
	text := formatDownloadProgress("EPUB", 1, 3, 4*mb, 10*mb, 2*mb)
	for _, wanted := range []string{"EPUB", "1/3", "40%", "4 MB / 10 MB", "2 MB/s"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("progress is missing %q:\n%s", wanted, text)
		}
	}

	unknown := formatDownloadProgress("FB2", 2, 3, 4*mb, -1, 0)
	if !strings.Contains(unknown, "4 MB скачано") {
		t.Fatalf("unknown-size progress is wrong:\n%s", unknown)
	}

	over := formatDownloadProgress("EPUB", 1, 1, 12*mb, 10*mb, 0)
	if !strings.Contains(over, "100%") {
		t.Fatalf("progress was not clamped:\n%s", over)
	}
}
