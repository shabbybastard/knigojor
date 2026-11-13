// Package books downloads books and produces/validates EPUB files.
package books

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// EPUBAuthor is one author or translator in EPUB metadata.
type EPUBAuthor struct {
	Name     string
	SortName string
	Role     string
}

// EPUBImage is an image embedded in a generated EPUB.
type EPUBImage struct {
	Path      string
	MediaType string
	Data      []byte
}

// EPUBChapter is one chapter of a generated EPUB.
type EPUBChapter struct {
	Title string
	Level int
	Body  string
}

// EPUBBook is the data needed to write an EPUB.
type EPUBBook struct {
	Identifier      string
	Title           string
	Language        string
	Annotation      string
	Authors         []EPUBAuthor
	Translators     []string
	Genres          []string
	Keywords        string
	Date            string
	Publisher       string
	City            string
	PublishYear     string
	Source          string
	Rights          string
	ISBN            string
	Series          string
	SeriesIndex     int
	DocumentID      string
	DocumentAuthor  string
	DocumentDate    string
	PublishBookName string
	Version         string
	ProgramUsed     string
	SrcLang         string
	SrcOCR          string
	History         string
	CustomInfo      string
	CoverData       []byte
	CoverExt        string
	Images          []EPUBImage
	Chapters        []EPUBChapter
}

type containerFile struct {
	XMLName   xml.Name `xml:"container"`
	Rootfiles struct {
		Rootfile []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfile"`
	} `xml:"rootfiles"`
}

type packageFile struct {
	XMLName  xml.Name `xml:"package"`
	Manifest *struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine *struct {
		ItemRefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

const (
	maxEPUBEntries      = 5000
	maxEPUBUncompressed = 256 * 1024 * 1024
)

// ValidateEPUB checks the ZIP container and the OPF manifest/spine.
func ValidateEPUB(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil || info.Size() == 0 {
		return false
	}
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return false
	}
	defer reader.Close()
	if len(reader.File) > maxEPUBEntries {
		return false
	}

	names := make(map[string]*zip.File, len(reader.File))
	remaining := int64(maxEPUBUncompressed)
	for _, file := range reader.File {
		if _, exists := names[file.Name]; exists {
			return false
		}
		names[file.Name] = file
		if file.UncompressedSize64 > uint64(remaining) {
			return false
		}
		// Detect CRC errors and enforce the cumulative size even if the
		// archive lied about uncompressed sizes.
		rc, err := file.Open()
		if err != nil {
			return false
		}
		written, copyErr := io.Copy(io.Discard, io.LimitReader(rc, remaining+1))
		closeErr := rc.Close()
		if copyErr != nil || closeErr != nil || written > remaining {
			return false
		}
		remaining -= written
	}

	mimetypeFile, ok := names["mimetype"]
	if !ok {
		return false
	}
	mimetype, err := readZipFile(mimetypeFile)
	if err != nil || strings.TrimSpace(string(mimetype)) != "application/epub+zip" {
		return false
	}

	containerEntry, ok := names["META-INF/container.xml"]
	if !ok {
		return false
	}
	containerBytes, err := readZipFile(containerEntry)
	if err != nil {
		return false
	}
	var container containerFile
	if err := xml.Unmarshal(containerBytes, &container); err != nil {
		return false
	}
	opfName := ""
	for _, rootfile := range container.Rootfiles.Rootfile {
		if rootfile.FullPath != "" {
			opfName = rootfile.FullPath
			break
		}
	}
	if opfName == "" || names[opfName] == nil {
		opfName = ""
		for name := range names {
			if strings.HasSuffix(strings.ToLower(name), ".opf") {
				opfName = name
				break
			}
		}
		if opfName == "" {
			return false
		}
	}

	opfBytes, err := readZipFile(names[opfName])
	if err != nil {
		return false
	}
	var pkg packageFile
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil || pkg.XMLName.Local != "package" {
		return false
	}
	if pkg.Manifest == nil || pkg.Spine == nil {
		return false
	}

	ids := make(map[string]bool)
	hrefs := make(map[string]bool)
	opfDir := path.Dir(opfName)
	for _, item := range pkg.Manifest.Items {
		if item.ID == "" || item.Href == "" || ids[item.ID] || hrefs[item.Href] {
			return false
		}
		ids[item.ID] = true
		hrefs[item.Href] = true

		parsed, err := url.Parse(item.Href)
		if err != nil {
			return false
		}
		if parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "data" {
			continue
		}
		hrefPath, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return false
		}
		hrefPath = strings.TrimLeft(hrefPath, "/")
		if hrefPath == "" {
			return false
		}
		target := path.Clean(path.Join(opfDir, hrefPath))
		if strings.HasPrefix(target, "../") || target == ".." {
			return false
		}
		if _, ok := names[target]; !ok {
			return false
		}
	}
	for _, itemref := range pkg.Spine.ItemRefs {
		if !ids[itemref.IDRef] {
			return false
		}
	}
	return true
}

// WriteEPUB writes a minimal but valid EPUB 3 file.
func WriteEPUB(dest string, book *EPUBBook) error {
	if book.Language == "" {
		book.Language = "ru"
	}
	if book.Identifier == "" {
		book.Identifier = "knigojor-" + sanitizeID(book.Title)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(dest)
		}
	}()

	writer := zip.NewWriter(file)
	write := func(name string, data []byte, method uint16) error {
		header := &zip.FileHeader{Name: name, Method: method, Modified: time.Now()}
		part, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = part.Write(data)
		return err
	}

	if err := write("mimetype", []byte("application/epub+zip"), zip.Store); err != nil {
		return err
	}
	if err := write("META-INF/container.xml", []byte(containerXML()), zip.Deflate); err != nil {
		return err
	}
	if err := write("EPUB/content.opf", []byte(contentOPF(book)), zip.Deflate); err != nil {
		return err
	}
	if err := write("EPUB/nav.xhtml", []byte(navXHTML(book)), zip.Deflate); err != nil {
		return err
	}
	if err := write("EPUB/toc.ncx", []byte(tocNCX(book)), zip.Deflate); err != nil {
		return err
	}
	if len(book.CoverData) > 0 {
		name := "EPUB/cover." + book.CoverExt
		if err := write(name, book.CoverData, zip.Deflate); err != nil {
			return err
		}
		if err := write("EPUB/cover.xhtml", []byte(coverXHTML(book)), zip.Deflate); err != nil {
			return err
		}
	}
	for _, image := range book.Images {
		if err := write("EPUB/"+image.Path, image.Data, zip.Deflate); err != nil {
			return err
		}
	}
	for index, chapter := range book.Chapters {
		name := fmt.Sprintf("EPUB/chap_%d.xhtml", index+1)
		if err := write(name, []byte(chapterXHTML(book, chapter)), zip.Deflate); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	success = true
	return nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func containerXML() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile media-type="application/oebps-package+xml" full-path="EPUB/content.opf"/>
  </rootfiles>
</container>
`
}

func contentOPF(book *EPUBBook) string {
	var manifest strings.Builder
	var spine strings.Builder

	if len(book.CoverData) > 0 {
		fmt.Fprintf(&manifest, `    <item href="cover.%s" id="cover-img" media-type="%s" properties="cover-image"/>
`, esc(book.CoverExt), coverMediaType(book.CoverExt))
		manifest.WriteString(`    <item href="cover.xhtml" id="cover" media-type="application/xhtml+xml"/>
`)
		spine.WriteString(`    <itemref idref="cover"/>
`)
	}
	for index, image := range book.Images {
		fmt.Fprintf(&manifest, `    <item href="%s" id="image-%d" media-type="%s"/>
`, escAttr(image.Path), index+1, escAttr(image.MediaType))
	}
	for index := range book.Chapters {
		fmt.Fprintf(&manifest, `    <item href="chap_%d.xhtml" id="chapter_%d" media-type="application/xhtml+xml"/>
`, index+1, index)
		fmt.Fprintf(&spine, `    <itemref idref="chapter_%d"/>
`, index)
	}
	manifest.WriteString(`    <item href="toc.ncx" id="ncx" media-type="application/x-dtbncx+xml"/>
    <item href="nav.xhtml" id="nav" media-type="application/xhtml+xml" properties="nav"/>
`)

	var creators strings.Builder
	creatorIndex := 0
	for _, author := range book.Authors {
		if strings.TrimSpace(author.Name) == "" {
			continue
		}
		creatorIndex++
		id := fmt.Sprintf("creator-%d", creatorIndex)
		role := author.Role
		if role == "" {
			role = "aut"
		}
		fmt.Fprintf(&creators, `    <dc:creator id="%s">%s</dc:creator>
`, id, esc(author.Name))
		fmt.Fprintf(&creators, `    <meta refines="#%s" property="role" scheme="marc:relators">%s</meta>
`, id, escAttr(role))
		if author.SortName != "" {
			fmt.Fprintf(&creators, `    <meta refines="#%s" property="file-as">%s</meta>
`, id, esc(author.SortName))
		}
	}
	translatorIndex := 0
	for _, translator := range book.Translators {
		if strings.TrimSpace(translator) == "" {
			continue
		}
		translatorIndex++
		id := fmt.Sprintf("translator-%d", translatorIndex)
		fmt.Fprintf(&creators, `    <dc:contributor id="%s">%s</dc:contributor>
`, id, esc(translator))
		fmt.Fprintf(&creators, `    <meta refines="#%s" property="role" scheme="marc:relators">trl</meta>
`, id)
	}

	var subjects strings.Builder
	for _, genre := range book.Genres {
		if strings.TrimSpace(genre) == "" {
			continue
		}
		fmt.Fprintf(&subjects, "    <dc:subject>%s</dc:subject>\n", esc(genre))
	}

	description := ""
	if book.Annotation != "" {
		description = fmt.Sprintf("    <dc:description>%s</dc:description>\n", esc(book.Annotation))
	}
	date := ""
	if book.Date != "" {
		date = fmt.Sprintf("    <dc:date>%s</dc:date>\n", esc(normalizeDate(book.Date)))
	}
	publisher := ""
	if book.Publisher != "" {
		publisher = fmt.Sprintf("    <dc:publisher>%s</dc:publisher>\n", esc(book.Publisher))
	}
	source := ""
	if book.Source != "" {
		source = fmt.Sprintf("    <dc:source>%s</dc:source>\n", esc(book.Source))
	}
	rights := ""
	if book.Rights != "" {
		rights = fmt.Sprintf("    <dc:rights>%s</dc:rights>\n", esc(book.Rights))
	}
	isbn := ""
	if book.ISBN != "" {
		isbn = fmt.Sprintf("    <dc:identifier>urn:isbn:%s</dc:identifier>\n", esc(book.ISBN))
	}
	series := ""
	if book.Series != "" {
		series = fmt.Sprintf(`    <meta property="belongs-to-collection" id="series">%s</meta>
    <meta refines="#series" property="collection-type">series</meta>
`, esc(book.Series))
		if book.SeriesIndex > 0 {
			series += fmt.Sprintf(`    <meta refines="#series" property="group-position">%d</meta>
`, book.SeriesIndex)
		}
	}
	keywords := ""
	if book.Keywords != "" {
		keywords = fmt.Sprintf("    <meta property=\"keywords\">%s</meta>\n", esc(book.Keywords))
	}
	custom := ""
	for _, item := range []struct {
		name  string
		value string
	}{
		{"knigojor:city", book.City},
		{"knigojor:publish-year", book.PublishYear},
		{"knigojor:document-id", book.DocumentID},
		{"knigojor:document-author", book.DocumentAuthor},
		{"knigojor:document-date", book.DocumentDate},
		{"knigojor:publish-book-name", book.PublishBookName},
		{"knigojor:version", book.Version},
		{"knigojor:program-used", book.ProgramUsed},
		{"knigojor:src-lang", book.SrcLang},
		{"knigojor:src-ocr", book.SrcOCR},
		{"knigojor:history", book.History},
		{"knigojor:custom-info", book.CustomInfo},
	} {
		if item.value == "" {
			continue
		}
		custom += fmt.Sprintf("    <meta property=\"%s\">%s</meta>\n", item.name, esc(item.value))
	}
	coverMeta := ""
	if len(book.CoverData) > 0 {
		coverMeta = `    <meta name="cover" content="cover-img"/>
`
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="id" version="3.0" prefix="knigojor: https://knigojor.local/ns#">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <meta property="dcterms:modified">%s</meta>
    <meta name="generator" content="knigojor"/>
    <dc:identifier id="id">%s</dc:identifier>
%s    <dc:title>%s</dc:title>
    <dc:language>%s</dc:language>
%s%s%s%s%s%s%s%s%s%s%s  </metadata>
  <manifest>
%s  </manifest>
  <spine toc="ncx">
    <itemref idref="nav"/>
%s  </spine>
</package>
`, time.Now().UTC().Format("2006-01-02T15:04:05Z"), esc(book.Identifier), isbn, esc(book.Title),
		esc(book.Language), creators.String(), description, subjects.String(), date, publisher,
		source, rights, series, keywords, custom, coverMeta, manifest.String(), spine.String())
}

func navXHTML(book *EPUBBook) string {
	var items strings.Builder
	for index, chapter := range book.Chapters {
		title := chapter.Title
		if title == "" {
			title = fmt.Sprintf("Глава %d", index+1)
		}
		fmt.Fprintf(&items, `        <li><a href="chap_%d.xhtml">%s</a></li>
`, index+1, esc(title))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="%s" xml:lang="%s">
  <head><title>%s</title></head>
  <body>
    <nav epub:type="toc" id="toc" role="doc-toc">
      <h2>%s</h2>
      <ol>
%s      </ol>
    </nav>
  </body>
</html>
`, escAttr(book.Language), escAttr(book.Language), esc(book.Title), esc(book.Title), items.String())
}

func tocNCX(book *EPUBBook) string {
	var points strings.Builder
	for index, chapter := range book.Chapters {
		title := chapter.Title
		if title == "" {
			title = fmt.Sprintf("Глава %d", index+1)
		}
		fmt.Fprintf(&points, `    <navPoint id="chap%d">
      <navLabel><text>%s</text></navLabel>
      <content src="chap_%d.xhtml"/>
    </navPoint>
`, index+1, esc(title), index+1)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta content="%s" name="dtb:uid"/>
    <meta content="0" name="dtb:depth"/>
    <meta content="0" name="dtb:totalPageCount"/>
    <meta content="0" name="dtb:maxPageNumber"/>
  </head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
%s  </navMap>
</ncx>
`, escAttr(book.Identifier), esc(book.Title), points.String())
}

func coverXHTML(book *EPUBBook) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" lang="%s" xml:lang="%s">
  <head><title>Cover</title></head>
  <body><img src="cover.%s" alt="Cover" style="height:100%%; text-align:center"/></body>
</html>
`, escAttr(book.Language), escAttr(book.Language), escAttr(book.CoverExt))
}

func chapterXHTML(book *EPUBBook, chapter EPUBChapter) string {
	title := chapter.Title
	if title == "" {
		title = "Глава"
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" lang="%s" xml:lang="%s">
  <head><title>%s</title></head>
  <body><h2>%s</h2>
%s</body>
</html>
`, escAttr(book.Language), escAttr(book.Language), esc(title), esc(title), chapter.Body)
}

func coverMediaType(ext string) string {
	if strings.EqualFold(ext, "png") {
		return "image/png"
	}
	if strings.EqualFold(ext, "gif") {
		return "image/gif"
	}
	if strings.EqualFold(ext, "webp") {
		return "image/webp"
	}
	if strings.EqualFold(ext, "svg") {
		return "image/svg+xml"
	}
	return "image/jpeg"
}

func sanitizeID(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "book"
	}
	return result
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 4 {
		return value + "-01-01"
	}
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

func esc(value string) string {
	return html.EscapeString(value)
}

func escAttr(value string) string {
	return html.EscapeString(value)
}
