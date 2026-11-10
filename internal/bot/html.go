package bot

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

var invalidFilename = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// Esc escapes text for Telegram HTML. With quote=true it also escapes double
// quotes, which is needed for href attributes.
func Esc(value any, quote ...bool) string {
	if value == nil {
		return ""
	}
	text := fmt.Sprint(value)
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	if len(quote) > 0 && quote[0] {
		text = strings.ReplaceAll(text, `"`, "&quot;")
	}
	return text
}

// Clip truncates a value to limit runes, adding an ellipsis.
func Clip(value any, limit int) string {
	text := ""
	if value != nil {
		text = fmt.Sprint(value)
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return "…"
	}
	return strings.TrimRight(string(runes[:limit-1]), " ") + "…"
}

func utf16Len(value string) int {
	length := 0
	for _, r := range value {
		size := utf16.RuneLen(r)
		if size < 1 {
			size = 1
		}
		length += size
	}
	return length
}

func cutUTF16(value string, limit int) string {
	var builder strings.Builder
	used := 0
	for _, r := range value {
		size := utf16.RuneLen(r)
		if size < 1 {
			size = 1
		}
		if used+size > limit {
			break
		}
		builder.WriteRune(r)
		used += size
	}
	return builder.String()
}

// FilenameFor builds a safe EPUB filename.
func FilenameFor(title string, bookID int64) string {
	safe := invalidFilename.ReplaceAllString(title, "_")
	safe = strings.Trim(strings.TrimSpace(safe), ".")
	safe = Clip(safe, 100)
	if safe == "" {
		safe = fmt.Sprintf("%d", bookID)
	}
	return safe + ".epub"
}
