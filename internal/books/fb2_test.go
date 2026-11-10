package books

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

func TestConvertWindows1251FB2(t *testing.T) {
	source := `<?xml version="1.0" encoding="windows-1251"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
  <description>
    <title-info>
      <book-title>Тестовая книга</book-title>
      <author><first-name>Иван</first-name><last-name>Петров</last-name></author>
      <lang>ru</lang>
    </title-info>
  </description>
  <body>
    <section>
      <title>Глава</title>
      <p>Привет, мир.</p>
    </section>
  </body>
</FictionBook>`
	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fb2Path := filepath.Join(dir, "cp1251.fb2")
	if err := os.WriteFile(fb2Path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "book.epub")
	if err := ConvertFB2ToEPUB(fb2Path, out); err != nil {
		t.Fatal(err)
	}
	if !ValidateEPUB(out) {
		t.Fatal("generated EPUB is not valid")
	}
	archive, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	chapter := readEntry(t, archive, "EPUB/chap_1.xhtml")
	if !strings.Contains(chapter, "Привет, мир.") {
		t.Fatalf("chapter text was not decoded: %s", chapter)
	}
	opf := readEntry(t, archive, "EPUB/content.opf")
	if !strings.Contains(opf, "Тестовая книга") {
		t.Fatalf("title was not decoded: %s", opf)
	}
}
