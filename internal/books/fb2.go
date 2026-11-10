package books

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

type xmlNode struct {
	Name  xml.Name
	Attr  []xml.Attr
	Parts []any // string or *xmlNode, preserving document order
}

func (n *xmlNode) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	n.Name = start.Name
	n.Attr = append(n.Attr[:0], start.Attr...)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			child := &xmlNode{}
			if err := child.UnmarshalXML(decoder, value); err != nil {
				return err
			}
			n.Parts = append(n.Parts, child)
		case xml.CharData:
			if len(value) > 0 {
				n.Parts = append(n.Parts, string(value))
			}
		case xml.EndElement:
			return nil
		}
	}
}

func (n *xmlNode) text() string {
	if n == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range n.Parts {
		switch value := part.(type) {
		case string:
			builder.WriteString(value)
		case *xmlNode:
			builder.WriteString(value.text())
		}
	}
	return builder.String()
}

func (n *xmlNode) child(name string) *xmlNode {
	if n == nil {
		return nil
	}
	for _, part := range n.Parts {
		child, ok := part.(*xmlNode)
		if ok && child.Name.Local == name {
			return child
		}
	}
	return nil
}

func (n *xmlNode) children(name string) []*xmlNode {
	if n == nil {
		return nil
	}
	out := make([]*xmlNode, 0)
	for _, part := range n.Parts {
		child, ok := part.(*xmlNode)
		if ok && child.Name.Local == name {
			out = append(out, child)
		}
	}
	return out
}

func (n *xmlNode) findAll(name string) []*xmlNode {
	out := make([]*xmlNode, 0)
	var walk func(node *xmlNode)
	walk = func(node *xmlNode) {
		if node == nil {
			return
		}
		if node.Name.Local == name {
			out = append(out, node)
		}
		for _, part := range node.Parts {
			if child, ok := part.(*xmlNode); ok {
				walk(child)
			}
		}
	}
	walk(n)
	return out
}

func (n *xmlNode) attr(name string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

type fb2Binary struct {
	data        []byte
	contentType string
}

type fb2Converter struct {
	book     *EPUBBook
	binaries map[string]fb2Binary
	images   map[string]string
	anchors  map[string]string
	chapter  int
}

// ConvertFB2ToEPUB parses an FB2 file and writes a validated EPUB.
func ConvertFB2ToEPUB(fb2Path, epubPath string) error {
	return ConvertFB2ToEPUBProgress(fb2Path, epubPath, nil)
}

// ConvertFB2ToEPUBProgress is ConvertFB2ToEPUB with stage notifications.
func ConvertFB2ToEPUBProgress(fb2Path, epubPath string, progress func(string)) error {
	report := func(stage string) {
		if progress != nil {
			progress(stage)
		}
	}
	report("Разбор FB2...")

	file, err := os.Open(fb2Path)
	if err != nil {
		return fmt.Errorf("open FB2: %w", err)
	}
	defer file.Close()

	decoder := xml.NewDecoder(file)
	decoder.Strict = false
	decoder.Entity = xml.HTMLEntity
	decoder.CharsetReader = charsetReader

	root := &xmlNode{}
	if err := decoder.Decode(root); err != nil {
		return fmt.Errorf("parse FB2: %w", err)
	}

	converter := &fb2Converter{
		book:     &EPUBBook{Language: "ru"},
		binaries: parseBinaries(root),
		images:   make(map[string]string),
		anchors:  make(map[string]string),
	}
	report("Подготовка метаданных...")
	if err := converter.parseMetadata(root); err != nil {
		return err
	}
	if converter.book.Identifier == "" {
		converter.book.Identifier = "knigojor-" + strings.TrimSuffix(filepath.Base(fb2Path), filepath.Ext(fb2Path))
	}
	report("Сборка глав...")
	converter.buildChapters(root)
	if len(converter.book.Chapters) == 0 {
		converter.book.Chapters = []EPUBChapter{{
			Title: "Текст",
			Body:  "<p>Книга не содержит текста.</p>",
		}}
	}
	converter.resolveLinks()

	report("Сборка EPUB...")
	if err := WriteEPUB(epubPath, converter.book); err != nil {
		return err
	}
	report("Проверка EPUB...")
	if !ValidateEPUB(epubPath) {
		_ = os.Remove(epubPath)
		return fmt.Errorf("generated EPUB failed validation")
	}
	return nil
}

func (c *fb2Converter) parseMetadata(root *xmlNode) error {
	description := root.child("description")
	titleInfo := description.child("title-info")
	if titleInfo == nil {
		return fmt.Errorf("FB2 has no description/title-info")
	}

	c.book.Title = strings.TrimSpace(titleInfo.child("book-title").text())
	if c.book.Title == "" {
		c.book.Title = "Untitled"
	}
	if lang := strings.TrimSpace(titleInfo.child("lang").text()); lang != "" {
		c.book.Language = lang
	}
	c.book.SrcLang = strings.TrimSpace(titleInfo.child("src-lang").text())
	c.book.Annotation = annotationText(titleInfo)
	c.book.Keywords = strings.TrimSpace(titleInfo.child("keywords").text())
	c.book.Date = dateValue(titleInfo.child("date"))
	c.book.Series, c.book.SeriesIndex = sequenceInfo(titleInfo)

	for _, author := range titleInfo.children("author") {
		name, sortName := authorName(author)
		if name != "" {
			c.book.Authors = append(c.book.Authors, EPUBAuthor{Name: name, SortName: sortName, Role: "aut"})
		}
	}
	for _, translator := range titleInfo.children("translator") {
		name, _ := authorName(translator)
		if name != "" {
			c.book.Translators = append(c.book.Translators, name)
		}
	}
	for _, genre := range titleInfo.children("genre") {
		if text := strings.TrimSpace(genre.text()); text != "" {
			c.book.Genres = append(c.book.Genres, text)
		}
	}

	if cover := titleInfo.child("coverpage"); cover != nil {
		if image := cover.child("image"); image != nil {
			href := strings.TrimPrefix(strings.TrimSpace(image.attr("href")), "#")
			if binary, ok := c.binaries[href]; ok {
				c.book.CoverData = binary.data
				c.book.CoverExt = coverExtension(binary.contentType)
			}
		}
	}

	documentInfo := description.child("document-info")
	if documentInfo != nil {
		c.book.DocumentID = strings.TrimSpace(documentInfo.child("id").text())
		c.book.DocumentDate = dateValue(documentInfo.child("date"))
		c.book.Version = strings.TrimSpace(documentInfo.child("version").text())
		c.book.ProgramUsed = strings.TrimSpace(documentInfo.child("program-used").text())
		c.book.Source = strings.TrimSpace(documentInfo.child("src-url").text())
		c.book.SrcOCR = strings.TrimSpace(documentInfo.child("src-ocr").text())
		c.book.History = strings.TrimSpace(documentInfo.child("history").text())
		if author := documentInfo.child("author"); author != nil {
			c.book.DocumentAuthor, _ = authorName(author)
		}
	}

	publishInfo := description.child("publish-info")
	if publishInfo != nil {
		if publisher := strings.TrimSpace(publishInfo.child("publisher").text()); publisher != "" {
			c.book.Publisher = publisher
		}
		c.book.City = strings.TrimSpace(publishInfo.child("city").text())
		c.book.PublishYear = strings.TrimSpace(publishInfo.child("year").text())
		c.book.ISBN = strings.TrimSpace(publishInfo.child("isbn").text())
		c.book.PublishBookName = strings.TrimSpace(publishInfo.child("book-name").text())
		if c.book.Title == "Untitled" && c.book.PublishBookName != "" {
			c.book.Title = c.book.PublishBookName
		}
	}

	custom := make([]string, 0)
	for _, info := range description.children("custom-info") {
		value := strings.TrimSpace(info.text())
		if value == "" {
			continue
		}
		if kind := strings.TrimSpace(info.attr("info-type")); kind != "" {
			custom = append(custom, kind+": "+value)
		} else {
			custom = append(custom, value)
		}
	}
	c.book.CustomInfo = strings.Join(custom, "\n")

	if c.book.DocumentID != "" {
		c.book.Identifier = c.book.DocumentID
	}
	return nil
}

func (c *fb2Converter) buildChapters(root *xmlNode) {
	for _, body := range root.children("body") {
		bodyName := strings.ToLower(strings.TrimSpace(body.attr("name")))
		bodyTitle := plainText(body.child("title"))
		if bodyTitle == "" && bodyName == "notes" {
			bodyTitle = "Примечания"
		}
		sections := body.children("section")
		if len(sections) == 0 {
			c.addChapter(bodyTitle, 0, body)
			continue
		}
		if hasDirectBlocks(body) {
			c.addChapter(bodyTitle, 0, body)
		}
		for _, section := range sections {
			c.addSection(section, 0)
		}
	}
}

func (c *fb2Converter) addSection(section *xmlNode, level int) {
	title := plainText(section.child("title"))
	c.addChapter(title, level, section)
	for _, child := range section.children("section") {
		c.addSection(child, level+1)
	}
}

func (c *fb2Converter) addChapter(title string, level int, node *xmlNode) {
	index := len(c.book.Chapters)
	c.chapter = index
	if title == "" {
		title = fmt.Sprintf("Глава %d", index+1)
	}
	body := ""
	if id := anchorID(node.attr("id")); id != "" {
		body = `<a id="` + escAttr(id) + `"></a>`
		c.anchors[id] = fmt.Sprintf("chap_%d.xhtml#%s", index+1, id)
	}
	body += c.blocks(node)
	c.book.Chapters = append(c.book.Chapters, EPUBChapter{
		Title: title,
		Level: level,
		Body:  body,
	})
}

func (c *fb2Converter) resolveLinks() {
	for index := range c.book.Chapters {
		for id, target := range c.anchors {
			c.book.Chapters[index].Body = strings.ReplaceAll(
				c.book.Chapters[index].Body,
				`href="#`+escAttr(id)+`"`,
				`href="`+escAttr(target)+`"`,
			)
		}
	}
}

func (c *fb2Converter) blocks(node *xmlNode) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range node.Parts {
		child, ok := part.(*xmlNode)
		if !ok {
			continue
		}
		switch child.Name.Local {
		case "section", "title":
			continue
		case "p":
			id := anchorID(child.attr("id"))
			if id != "" {
				c.anchors[id] = fmt.Sprintf("chap_%d.xhtml#%s", c.chapter+1, id)
				builder.WriteString(`<p id="` + escAttr(id) + `">`)
			} else {
				builder.WriteString("<p>")
			}
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</p>\n")
		case "subtitle":
			builder.WriteString(`<p class="subtitle"><strong>`)
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</strong></p>\n")
		case "v":
			builder.WriteString(`<p class="verse">`)
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</p>\n")
		case "empty-line":
			builder.WriteString(`<p class="empty-line">&#160;</p>` + "\n")
		case "epigraph":
			builder.WriteString(`<div class="epigraph">` + "\n")
			builder.WriteString(c.blocks(child))
			builder.WriteString("</div>\n")
		case "poem":
			builder.WriteString(c.poem(child))
		case "cite":
			builder.WriteString("<blockquote>\n")
			builder.WriteString(c.blocks(child))
			builder.WriteString("</blockquote>\n")
		case "table":
			builder.WriteString(c.table(child))
		case "text-author":
			builder.WriteString(`<p class="text-author">`)
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</p>\n")
		case "image":
			builder.WriteString("<p>" + c.inline(child) + "</p>\n")
		default:
			if hasBlockChildren(child) {
				builder.WriteString(c.blocks(child))
			} else {
				builder.WriteString("<p>" + c.inline(child) + "</p>\n")
			}
		}
	}
	return builder.String()
}

func (c *fb2Converter) poem(node *xmlNode) string {
	var builder strings.Builder
	builder.WriteString(`<div class="poem">` + "\n")
	for _, part := range node.Parts {
		child, ok := part.(*xmlNode)
		if !ok {
			continue
		}
		switch child.Name.Local {
		case "title":
			builder.WriteString(`<p class="poem-title"><strong>`)
			builder.WriteString(esc(plainText(child)))
			builder.WriteString("</strong></p>\n")
		case "stanza":
			builder.WriteString(`<div class="stanza">` + "\n")
			builder.WriteString(c.blocks(child))
			builder.WriteString("</div>\n")
		case "v":
			builder.WriteString(`<p class="verse">`)
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</p>\n")
		case "text-author":
			builder.WriteString(`<p class="text-author">`)
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</p>\n")
		case "subtitle":
			builder.WriteString(`<p class="subtitle"><strong>`)
			builder.WriteString(c.inlineChildren(child))
			builder.WriteString("</strong></p>\n")
		}
	}
	builder.WriteString("</div>\n")
	return builder.String()
}

func (c *fb2Converter) table(node *xmlNode) string {
	var builder strings.Builder
	builder.WriteString("<table>\n")
	for _, row := range node.children("tr") {
		builder.WriteString("<tr>")
		for _, cell := range row.Parts {
			cellNode, ok := cell.(*xmlNode)
			if !ok {
				continue
			}
			tag := "td"
			if cellNode.Name.Local == "th" {
				tag = "th"
			}
			if cellNode.Name.Local != "td" && cellNode.Name.Local != "th" {
				continue
			}
			builder.WriteString("<" + tag + ">")
			builder.WriteString(c.inlineChildren(cellNode))
			builder.WriteString("</" + tag + ">")
		}
		builder.WriteString("</tr>\n")
	}
	builder.WriteString("</table>\n")
	return builder.String()
}

func (c *fb2Converter) inlineChildren(node *xmlNode) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range node.Parts {
		switch value := part.(type) {
		case string:
			builder.WriteString(esc(value))
		case *xmlNode:
			builder.WriteString(c.inline(value))
		}
	}
	return builder.String()
}

func (c *fb2Converter) inline(node *xmlNode) string {
	if node == nil {
		return ""
	}
	switch node.Name.Local {
	case "strong":
		return "<strong>" + c.inlineChildren(node) + "</strong>"
	case "emphasis":
		return "<em>" + c.inlineChildren(node) + "</em>"
	case "strikethrough":
		return "<s>" + c.inlineChildren(node) + "</s>"
	case "sub":
		return "<sub>" + c.inlineChildren(node) + "</sub>"
	case "sup":
		return "<sup>" + c.inlineChildren(node) + "</sup>"
	case "code":
		return "<code>" + c.inlineChildren(node) + "</code>"
	case "style":
		className := anchorID(node.attr("name"))
		if className == "" {
			return c.inlineChildren(node)
		}
		return `<span class="style-` + escAttr(className) + `">` + c.inlineChildren(node) + "</span>"
	case "a":
		href := strings.TrimSpace(node.attr("href"))
		content := c.inlineChildren(node)
		if href == "" {
			return content
		}
		if strings.HasPrefix(href, "#") {
			target := anchorID(strings.TrimPrefix(href, "#"))
			if target == "" {
				return content
			}
			return `<a href="#` + escAttr(target) + `">` + content + "</a>"
		}
		if safeExternalLink(href) {
			return `<a href="` + escAttr(href) + `">` + content + "</a>"
		}
		return content
	case "image":
		return c.inlineImage(node)
	default:
		return c.inlineChildren(node)
	}
}

func (c *fb2Converter) inlineImage(node *xmlNode) string {
	href := strings.TrimPrefix(strings.TrimSpace(node.attr("href")), "#")
	if href == "" {
		return ""
	}
	if path, ok := c.images[href]; ok {
		return `<img src="` + escAttr(path) + `" alt=""/>`
	}
	binary, ok := c.binaries[href]
	if !ok || len(binary.data) == 0 {
		return ""
	}
	extension := imageExtension(binary.contentType)
	path := fmt.Sprintf("images/img_%d.%s", len(c.book.Images)+1, extension)
	c.book.Images = append(c.book.Images, EPUBImage{
		Path:      path,
		MediaType: binary.contentType,
		Data:      binary.data,
	})
	c.images[href] = path
	return `<img src="` + escAttr(path) + `" alt=""/>`
}

func hasDirectBlocks(node *xmlNode) bool {
	if node == nil {
		return false
	}
	for _, part := range node.Parts {
		child, ok := part.(*xmlNode)
		if !ok {
			continue
		}
		switch child.Name.Local {
		case "section", "title":
			continue
		default:
			return true
		}
	}
	return false
}

func hasBlockChildren(node *xmlNode) bool {
	if node == nil {
		return false
	}
	for _, part := range node.Parts {
		child, ok := part.(*xmlNode)
		if !ok {
			continue
		}
		switch child.Name.Local {
		case "p", "subtitle", "epigraph", "poem", "cite", "table", "empty-line", "text-author":
			return true
		}
	}
	return false
}

func parseBinaries(root *xmlNode) map[string]fb2Binary {
	binaries := make(map[string]fb2Binary)
	for _, binary := range root.children("binary") {
		id := binary.attr("id")
		if id == "" {
			continue
		}
		contentType := binary.attr("content-type")
		if contentType == "" {
			contentType = "image/jpeg"
		}
		encoded := strings.Join(strings.Fields(binary.text()), "")
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		binaries[id] = fb2Binary{data: data, contentType: contentType}
	}
	return binaries
}

func annotationText(titleInfo *xmlNode) string {
	annotation := titleInfo.child("annotation")
	if annotation == nil {
		return ""
	}
	paragraphs := make([]string, 0)
	for _, child := range annotation.children("p") {
		text := strings.TrimSpace(child.text())
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(paragraphs) == 0 {
		return strings.TrimSpace(annotation.text())
	}
	return strings.Join(paragraphs, "\n")
}

func authorName(node *xmlNode) (name, sortName string) {
	first := strings.TrimSpace(node.child("first-name").text())
	middle := strings.TrimSpace(node.child("middle-name").text())
	last := strings.TrimSpace(node.child("last-name").text())
	nickname := strings.TrimSpace(node.child("nickname").text())
	parts := nonEmpty(first, middle, last)
	name = strings.Join(parts, " ")
	if name == "" {
		name = nickname
	}
	if name == "" {
		name = strings.TrimSpace(node.text())
	}
	sortName = strings.TrimSpace(strings.Join(nonEmpty(last, first, middle), " "))
	return name, sortName
}

func dateValue(node *xmlNode) string {
	if node == nil {
		return ""
	}
	if value := strings.TrimSpace(node.attr("value")); value != "" {
		return value
	}
	return strings.TrimSpace(node.text())
}

func sequenceInfo(titleInfo *xmlNode) (string, int) {
	sequence := titleInfo.child("sequence")
	if sequence == nil {
		return "", 0
	}
	name := strings.TrimSpace(sequence.attr("name"))
	number, _ := strconv.Atoi(strings.TrimSpace(sequence.attr("number")))
	return name, number
}

func plainText(node *xmlNode) string {
	if node == nil {
		return ""
	}
	return strings.Join(strings.Fields(node.text()), " ")
}

func anchorID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func safeExternalLink(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return parsed.Host != "" || parsed.Scheme == "mailto"
	default:
		return false
	}
}

func imageExtension(contentType string) string {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "png"):
		return "png"
	case strings.Contains(contentType, "gif"):
		return "gif"
	case strings.Contains(contentType, "webp"):
		return "webp"
	case strings.Contains(contentType, "svg"):
		return "svg"
	default:
		return "jpg"
	}
}

func coverExtension(contentType string) string {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "png"):
		return "png"
	case strings.Contains(contentType, "gif"):
		return "gif"
	case strings.Contains(contentType, "webp"):
		return "webp"
	case strings.Contains(contentType, "svg"):
		return "svg"
	default:
		return "jpg"
	}
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func charsetReader(label string, input io.Reader) (io.Reader, error) {
	encoding, _ := charset.Lookup(label)
	if encoding == nil {
		return input, nil
	}
	return transform.NewReader(input, encoding.NewDecoder()), nil
}
