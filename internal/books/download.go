package books

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shabbybastard/knigojor/internal/netx"
)

// DownloadEPUB streams an EPUB to dest.
func DownloadEPUB(
	ctx context.Context,
	http *netx.Client,
	rawURL string,
	dest string,
	maxSizeMB int,
	timeout time.Duration,
) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	total, err := http.DownloadOnce(ctx, rawURL, dest, int64(maxSizeMB)*1024*1024, timeout)
	if err != nil {
		return "", err
	}
	if total == 0 {
		_ = os.Remove(dest)
		return "", fmt.Errorf("empty file")
	}
	return dest, nil
}

// DownloadFB2Zip downloads an FB2 archive and extracts its first .fb2 file.
func DownloadFB2Zip(
	ctx context.Context,
	http *netx.Client,
	rawURL string,
	destDir string,
	maxSizeMB int,
	timeout time.Duration,
) (string, error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	maxBytes := int64(maxSizeMB) * 1024 * 1024
	zipPath := filepath.Join(destDir, "book.fb2.zip")
	defer os.Remove(zipPath)

	total, err := http.DownloadOnce(ctx, rawURL, zipPath, maxBytes, timeout)
	if err != nil {
		return "", err
	}
	if total == 0 {
		return "", fmt.Errorf("empty file")
	}

	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open FB2 archive: %w", err)
	}
	defer archive.Close()

	if len(archive.File) > 1000 {
		return "", fmt.Errorf("archive contains too many files")
	}
	maxUncompressed := uint64(maxBytes)
	var uncompressed uint64
	for _, file := range archive.File {
		if file.UncompressedSize64 > maxUncompressed-uncompressed {
			return "", fmt.Errorf("archive is too large after extraction")
		}
		uncompressed += file.UncompressedSize64
	}

	var fb2 *zip.File
	for _, file := range archive.File {
		if strings.HasSuffix(strings.ToLower(file.Name), ".fb2") {
			fb2 = file
			break
		}
	}
	if fb2 == nil {
		return "", fmt.Errorf("no .fb2 in archive")
	}

	cleanName := filepath.Clean(filepath.FromSlash(fb2.Name))
	if filepath.IsAbs(cleanName) || cleanName == ".." ||
		strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe path in archive: %s", fb2.Name)
	}
	target := filepath.Join(destDir, cleanName)
	relative, err := filepath.Rel(destDir, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe path in archive: %s", fb2.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}

	source, err := fb2.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()
	destination, err := os.Create(target)
	if err != nil {
		return "", err
	}
	defer destination.Close()
	written, err := io.Copy(destination, io.LimitReader(source, maxBytes+1))
	if err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if written > maxBytes {
		_ = os.Remove(target)
		return "", fmt.Errorf("archive is too large after extraction")
	}
	return target, nil
}
