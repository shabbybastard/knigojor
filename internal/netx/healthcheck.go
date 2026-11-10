package netx

import (
	"context"
	"log/slog"
	"time"
)

// CheckMirror performs one HTTP health probe.
func CheckMirror(ctx context.Context, client *Client, rawURL string) bool {
	if client == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := client.Get(probeCtx, rawURL, 10*time.Second)
	if err != nil {
		slog.Debug("healthcheck failed", "url", rawURL, "error", err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

// HealthcheckLoop probes every mirror until the context is cancelled.
func HealthcheckLoop(ctx context.Context, mirrors *MirrorManager, direct, tor *Client, interval time.Duration) {
	if interval <= 0 {
		interval = 300 * time.Second
	}
	for {
		for _, mirror := range mirrors.AllMirrors() {
			client := direct
			if mirror.IsOnion() {
				client = tor
			}
			if client == nil {
				continue
			}
			if CheckMirror(ctx, client, mirror.URL()) {
				mirror.MarkSuccess()
			} else {
				mirror.MarkFailure("healthcheck failed")
			}
			mirrors.persist(ctx, mirror)
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
