package books

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertFB2ToEPUB(t *testing.T) {
	out := filepath.Join(t.TempDir(), "book.epub")
	if err := ConvertFB2ToEPUB(filepath.Join("..", "..", "testdata", "book_sample.fb2"), out); err != nil {
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
	names := make(map[string]bool)
	for _, file := range archive.File {
		if names[file.Name] {
			t.Fatalf("duplicate entry %s", file.Name)
		}
		names[file.Name] = true
	}
	if !names["EPUB/cover.png"] {
		t.Fatal("cover is missing")
	}
	chapter := readEntry(t, archive, "EPUB/chap_1.xhtml")
	if !strings.Contains(chapter, "<h2>Глава 1</h2>") {
		t.Fatalf("chapter heading missing: %s", chapter)
	}
	if strings.Contains(chapter, "<p>Глава 1</p>") {
		t.Fatal("chapter title was duplicated as a paragraph")
	}
	if !strings.Contains(chapter, "Первый абзац.") {
		t.Fatalf("paragraph missing: %s", chapter)
	}
	opf := readEntry(t, archive, "EPUB/content.opf")
	if !strings.Contains(opf, "Краткая аннотация") {
		t.Fatalf("annotation missing from OPF: %s", opf)
	}
	if !strings.Contains(opf, `idref="cover"`) {
		t.Fatalf("cover page is not reachable from the spine: %s", opf)
	}
}

func TestValidateRejectsBrokenFiles(t *testing.T) {
	if ValidateEPUB(filepath.Join(t.TempDir(), "nope.epub")) {
		t.Fatal("missing file accepted")
	}
	broken := filepath.Join(t.TempDir(), "broken.epub")
	if err := os.WriteFile(broken, []byte("not an epub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ValidateEPUB(broken) {
		t.Fatal("non-zip accepted")
	}
}

func TestValidateRejectsMissingManifestResource(t *testing.T) {
	valid := filepath.Join(t.TempDir(), "valid.epub")
	if err := ConvertFB2ToEPUB(filepath.Join("..", "..", "testdata", "book_sample.fb2"), valid); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "broken.epub")
	source, err := zip.OpenReader(valid)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(broken)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(target)
	for _, file := range source.File {
		if file.Name == "EPUB/chap_1.xhtml" {
			continue
		}
		header := &zip.FileHeader{Name: file.Name, Method: file.Method}
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		data, err := readZipFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	if ValidateEPUB(broken) {
		t.Fatal("EPUB with a missing manifest resource accepted")
	}
}

func TestValidateRequiresManifestAndSpine(t *testing.T) {
	tests := []string{
		`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/></package>`,
		`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item href="nav.xhtml" id="nav" media-type="application/xhtml+xml"/></manifest></package>`,
	}
	for _, opf := range tests {
		path := filepath.Join(t.TempDir(), "broken.epub")
		writeRawEPUB(t, path, opf)
		if ValidateEPUB(path) {
			t.Fatalf("EPUB with incomplete OPF accepted:\n%s", opf)
		}
	}
}

func writeRawEPUB(t *testing.T, path, opf string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	write := func(name, data string) {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	write("mimetype", "application/epub+zip")
	write("META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="EPUB/content.opf"/></rootfiles></container>`)
	write("EPUB/content.opf", opf)
	write("EPUB/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body/></html>`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readEntry(t *testing.T, archive *zip.ReadCloser, name string) string {
	t.Helper()
	for _, file := range archive.File {
		if file.Name == name {
			data, err := readZipFile(file)
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
	}
	t.Fatalf("entry %s not found", name)
	return ""
}
