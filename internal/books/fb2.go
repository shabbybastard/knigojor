package books

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// ConvertFB2ToEPUB parses an FB2 file and writes a validated EPUB.
func ConvertFB2ToEPUB(fb2Path, epubPath string) error {
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

	titleInfo := root.findAll("title-info")
	var info *xmlNode
	if len(titleInfo) > 0 {
		info = titleInfo[0]
	}

	book := &EPUBBook{
		Title:      "Untitled",
		Language:   "ru",
		Identifier: "knigojor-" + strings.TrimSuffix(filepath.Base(fb2Path), filepath.Ext(fb2Path)),
	}
	if info != nil {
		if title := strings.TrimSpace(info.child("book-title").text()); title != "" {
			book.Title = title
		}
		for _, author := range info.children("author") {
			first := strings.TrimSpace(author.child("first-name").text())
			middle := strings.TrimSpace(author.child("middle-name").text())
			last := strings.TrimSpace(author.child("last-name").text())
			name := strings.Join(nonEmpty(first, middle, last), " ")
			if name != "" {
				book.Authors = append(book.Authors, name)
			}
		}
		if lang := strings.TrimSpace(info.child("lang").text()); lang != "" {
			book.Language = lang
		}
		book.Annotation = strings.TrimSpace(info.child("annotation").text())
	}

	binaries := make(map[string]struct {
		data        []byte
		contentType string
	})
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
		binaries[id] = struct {
			data        []byte
			contentType string
		}{data: data, contentType: contentType}
	}

	if info != nil {
		if cover := info.child("coverpage"); cover != nil {
			if image := cover.child("image"); image != nil {
				href := strings.TrimPrefix(image.attr("href"), "#")
				if binary, ok := binaries[href]; ok {
					book.CoverData = binary.data
					book.CoverExt = coverExtension(binary.contentType)
				}
			}
		}
	}

	body := root.child("body")
	sections := body.children("section")
	if len(sections) == 0 && body != nil {
		sections = []*xmlNode{body}
	}
	for index, section := range sections {
		chapter := EPUBChapter{Title: strings.TrimSpace(section.child("title").text())}
		if chapter.Title == "" {
			chapter.Title = fmt.Sprintf("Глава %d", index+1)
		}
		collectParagraphs(section, false, &chapter.Paragraphs)
		book.Chapters = append(book.Chapters, chapter)
	}

	if err := WriteEPUB(epubPath, book); err != nil {
		return err
	}
	if !ValidateEPUB(epubPath) {
		_ = os.Remove(epubPath)
		return fmt.Errorf("generated EPUB failed validation")
	}
	return nil
}

func collectParagraphs(node *xmlNode, inTitle bool, out *[]string) {
	if node == nil {
		return
	}
	for _, part := range node.Parts {
		child, ok := part.(*xmlNode)
		if !ok {
			continue
		}
		childInTitle := inTitle || child.Name.Local == "title"
		if child.Name.Local == "p" && !childInTitle {
			text := strings.TrimSpace(child.text())
			if text != "" {
				*out = append(*out, text)
			}
		}
		collectParagraphs(child, childInTitle, out)
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
