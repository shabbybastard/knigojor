package bot

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// formatUptime renders a duration without seconds.
func formatUptime(duration time.Duration) string {
	if duration < time.Minute {
		return "< 1 мин"
	}
	minutes := int(duration / time.Minute)
	days := minutes / (24 * 60)
	minutes %= 24 * 60
	hours := minutes / 60
	minutes %= 60

	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d д", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d ч", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d мин", minutes))
	}
	if len(parts) == 0 {
		return "< 1 мин"
	}
	return strings.Join(parts, " ")
}

// formatBytes renders a byte count using binary units.
func formatBytes(value uint64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case value >= gb:
		return fmt.Sprintf("%.1f GB", float64(value)/float64(gb))
	case value >= mb:
		return fmt.Sprintf("%.0f MB", float64(value)/float64(mb))
	case value >= kb:
		return fmt.Sprintf("%.0f KB", float64(value)/float64(kb))
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func readLoadAverage() (string, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "", false
	}
	return fields[0] + " / " + fields[1] + " / " + fields[2], true
}

func readMemoryUsage() (free, total uint64, ok bool) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()

	var memTotal, memAvailable, memFree uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			memTotal = value * 1024
		case "MemAvailable":
			memAvailable = value * 1024
		case "MemFree":
			memFree = value * 1024
		}
	}
	if err := scanner.Err(); err != nil || memTotal == 0 {
		return 0, 0, false
	}
	if memAvailable == 0 {
		memAvailable = memFree
	}
	return memAvailable, memTotal, true
}

func readProcessRSS() (uint64, bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "VmRSS:" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return value * 1024, true
	}
	return 0, false
}

func readDiskUsage(path string) (free, total uint64, ok bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	free = stat.Bavail * uint64(stat.Bsize)
	total = stat.Blocks * uint64(stat.Bsize)
	return free, total, true
}
