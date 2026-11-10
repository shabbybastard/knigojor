package bot

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const downloadProgressInterval = time.Second

func formatDownloadProgress(label string, attempt, attempts int, written, total, speed int64) string {
	line := ""
	if total > 0 {
		percent := written * 100 / total
		if percent > 100 {
			percent = 100
		}
		line = fmt.Sprintf(
			"%d%% · %s / %s",
			percent,
			formatBytes(uint64(max(written, 0))),
			formatBytes(uint64(total)),
		)
	} else {
		line = formatBytes(uint64(max(written, 0))) + " скачано"
	}
	if speed > 0 {
		line += " · " + formatBytes(uint64(speed)) + "/s"
	}
	return fmt.Sprintf(
		"<b>FLIBUSTA / DOWNLOAD</b>\n\n📥 %s · попытка %d/%d\n%s",
		label,
		attempt,
		attempts,
		line,
	)
}

func fileSizeLabel(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 {
		return ""
	}
	return " (" + formatBytes(uint64(info.Size())) + ")"
}

// downloadProgress decouples HTTP reading from Telegram edits: the download
// callback only stores the latest byte counters, while a separate goroutine
// updates the UI at most once per interval.
type downloadProgress struct {
	status   func(string)
	label    string
	attempt  int
	attempts int

	mu           sync.Mutex
	written      int64
	total        int64
	shownWritten int64
	shownTotal   int64

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func startDownloadProgress(
	status func(string),
	label string,
	attempt, attempts int,
) *downloadProgress {
	progress := &downloadProgress{
		status:   status,
		label:    label,
		attempt:  attempt,
		attempts: attempts,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go progress.loop()
	return progress
}

func (p *downloadProgress) Update(written, total int64) {
	p.mu.Lock()
	p.written = written
	p.total = total
	p.mu.Unlock()
}

func (p *downloadProgress) Close() {
	p.once.Do(func() {
		close(p.stop)
		<-p.done
		p.mu.Lock()
		written, total := p.written, p.total
		shownWritten, shownTotal := p.shownWritten, p.shownTotal
		p.mu.Unlock()
		if total > 0 && written >= total && (written != shownWritten || total != shownTotal) {
			p.status(formatDownloadProgress(p.label, p.attempt, p.attempts, written, total, 0))
		}
	})
}

func (p *downloadProgress) loop() {
	defer close(p.done)
	ticker := time.NewTicker(downloadProgressInterval)
	defer ticker.Stop()

	var lastBytes int64
	var lastTotal int64
	var lastUpdate time.Time
	for {
		select {
		case <-p.stop:
			return
		case now := <-ticker.C:
			p.mu.Lock()
			written, total := p.written, p.total
			p.mu.Unlock()
			if written == lastBytes && total == lastTotal {
				continue
			}
			if written == 0 && total <= 0 {
				continue
			}
			var speed int64
			if !lastUpdate.IsZero() {
				elapsed := now.Sub(lastUpdate).Seconds()
				if elapsed > 0 {
					speed = int64(float64(written-lastBytes) / elapsed)
				}
			}
			lastBytes = written
			lastTotal = total
			lastUpdate = now
			p.mu.Lock()
			p.shownWritten = written
			p.shownTotal = total
			p.mu.Unlock()
			p.status(formatDownloadProgress(p.label, p.attempt, p.attempts, written, total, speed))
		}
	}
}
