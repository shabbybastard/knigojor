package flibusta

import "fmt"

// FB2URL returns the FB2 download URL for a book.
func FB2URL(mirrorURL string, bookID int64) string {
	return fmt.Sprintf("%s/b/%d/fb2", trimSlash(mirrorURL), bookID)
}

// EPUBURL returns the EPUB download URL for a book.
func EPUBURL(mirrorURL string, bookID int64) string {
	return fmt.Sprintf("%s/b/%d/epub", trimSlash(mirrorURL), bookID)
}

func trimSlash(value string) string {
	if len(value) > 0 && value[len(value)-1] == '/' {
		return value[:len(value)-1]
	}
	return value
}
