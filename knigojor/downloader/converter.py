"""FB2 -> EPUB converter (basic: metadata, cover, chapters, toc)."""

from __future__ import annotations

import base64
import logging
from pathlib import Path

from ebooklib import epub
from lxml import etree

log = logging.getLogger("knigojor.converter")

FB2_NS = "http://www.gribuser.ru/xml/fictionbook/2.0"
XLINK = "http://www.w3.org/1999/xlink"


def _text(el) -> str:
    if el is None:
        return ""
    return "".join(el.itertext()).strip()


def convert_fb2_to_epub(fb2_path: Path, epub_path: Path) -> Path:
    parser = etree.XMLParser(
        recover=True,
        encoding=None,
        resolve_entities=False,
        no_network=True,
        load_dtd=False,
    )
    tree = etree.parse(str(fb2_path), parser)
    root = tree.getroot()

    ns = {"fb": FB2_NS, "l": XLINK}

    # Metadata.
    title_info = root.find(".//{%s}title-info" % FB2_NS)
    book_title = ""
    authors: list[str] = []
    lang = "ru"
    annotation = ""
    cover_id = None

    if title_info is not None:
        bt = title_info.find("{%s}book-title" % FB2_NS)
        book_title = _text(bt) or "Untitled"
        for author in title_info.findall("{%s}author" % FB2_NS):
            first = _text(author.find("{%s}first-name" % FB2_NS))
            last = _text(author.find("{%s}last-name" % FB2_NS))
            middle = _text(author.find("{%s}middle-name" % FB2_NS))
            name = " ".join(x for x in (first, middle, last) if x).strip()
            if name:
                authors.append(name)
        lang_el = title_info.find("{%s}lang" % FB2_NS)
        if lang_el is not None and lang_el.text:
            lang = lang_el.text.strip()
        ann_el = title_info.find("{%s}annotation" % FB2_NS)
        if ann_el is not None:
            annotation = _text(ann_el)
        cover = title_info.find("{%s}coverpage" % FB2_NS)
        if cover is not None:
            img = cover.find("{%s}image" % FB2_NS)
            if img is not None:
                cover_id = img.get("{%s}href" % XLINK)
                if cover_id and cover_id.startswith("#"):
                    cover_id = cover_id[1:]

    # Binaries map.
    binaries: dict[str, tuple[bytes, str]] = {}
    for binary in root.findall("{%s}binary" % FB2_NS):
        binary_id = binary.get("id")
        content_type = binary.get("content-type", "image/jpeg")
        if binary_id is not None:
            try:
                binaries[binary_id] = (
                    base64.b64decode(binary.text or ""),
                    content_type,
                )
            except Exception:
                log.warning("failed to decode binary %s", binary_id)

    book = epub.EpubBook()
    book.set_identifier(f"knigojor-{fb2_path.stem}")
    book.set_title(book_title)
    book.set_language(lang)
    for author in authors:
        book.add_author(author)
    if annotation:
        book.add_metadata("DC", "description", annotation)

    # Cover.  ``set_cover`` adds both the image and the cover page; adding a
    # manual EpubItem as well would create a duplicate EPUB/cover.* entry.
    if cover_id and cover_id in binaries:
        data, content_type = binaries[cover_id]
        ext = "png" if "png" in content_type else "jpg"
        book.set_cover(f"cover.{ext}", data)

    # Chapters: top-level body sections.  Paragraphs inside <title> are
    # excluded because they are already rendered as the chapter heading.
    body = root.find("{%s}body" % FB2_NS)
    chapters = []
    toc = []
    if body is not None:
        sections = body.findall("{%s}section" % FB2_NS)
        if not sections:
            sections = [body]
        for i, section in enumerate(sections, start=1):
            section_title = ""
            title_el = section.find("{%s}title" % FB2_NS)
            if title_el is not None:
                section_title = _text(title_el)
            if not section_title:
                section_title = f"Глава {i}"

            paragraphs = section.xpath(
                ".//fb:p[not(ancestor::fb:title)]",
                namespaces=ns,
            )
            html_parts = [
                f"<p>{_escape(_text(p))}</p>"
                for p in paragraphs
                if _text(p)
            ]
            content = "\n".join(html_parts) if html_parts else "<p></p>"
            chapter = epub.EpubHtml(
                title=section_title,
                file_name=f"chap_{i}.xhtml",
                lang=lang,
            )
            chapter.content = (
                f"<h2>{_escape(section_title)}</h2>\n{content}".encode("utf-8")
            )
            book.add_item(chapter)
            chapters.append(chapter)
            toc.append(epub.Link(f"chap_{i}.xhtml", section_title, f"chap{i}"))

    book.toc = toc
    book.add_item(epub.EpubNcx())
    book.add_item(epub.EpubNav())

    spine = ["nav"] + chapters
    book.spine = spine

    epub.write_epub(str(epub_path), book, {})
    return epub_path


def _escape(text: str) -> str:
    return (
        text.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
    )
