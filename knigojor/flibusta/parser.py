"""HTML parsing for Flibusta pages."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Optional
from urllib.parse import urljoin

from selectolax.lexbor import LexborHTMLParser as HTMLParser

BASE = "https://flibusta.is"


@dataclass
class SearchResultBook:
    id: int
    title: str
    authors: list[str] = field(default_factory=list)
    url: str = ""


@dataclass
class SearchResults:
    books: list[SearchResultBook] = field(default_factory=list)
    total: int = 0
    page: int = 0
    has_next: bool = False
    has_prev: bool = False


def _abs(url: str, base: str = BASE) -> str:
    if url.startswith("http"):
        return url
    return urljoin(base.rstrip("/") + "/", url)


def _node_text(node, separator: str = " ") -> str:
    if node is None:
        return ""
    text = node.text(separator=separator, strip=True)
    return re.sub(r"\s+", " ", text).strip()


def parse_search(html: str, page: int = 0) -> SearchResults:
    tree = HTMLParser(html)
    res = SearchResults(page=page)

    # Total count from "Найденные книги (1 - 50 из 154)".
    results_list = None
    for h3 in tree.css("h3"):
        text = _node_text(h3)
        m = re.search(r"Найденные книги\s*\([^)]*из\s*(\d+)\s*\)", text)
        if m:
            res.total = int(m.group(1))
        if "Найденные книги" in text:
            node = h3.next
            while node is not None:
                if node.tag == "ul":
                    results_list = node
                    break
                if node.tag in ("h2", "h3"):
                    break
                node = node.next
            break

    if res.total == 0:
        m = re.search(r"Найденные книги[^)]*из\s*(\d+)", html)
        if m:
            res.total = int(m.group(1))

    # Prefer the actual result list; fall back to all list items.
    items = results_list.css("li") if results_list is not None else tree.css("li")
    seen_ids: set[int] = set()
    for li in items:
        book_link = None
        for a in li.css("a[href^='/b/']"):
            href = a.attributes.get("href", "")
            m = re.fullmatch(r"/b/(\d+)", href)
            if m:
                book_link = (int(m.group(1)), _node_text(a))
                break
        if book_link is None:
            continue
        bid, title = book_link
        if bid in seen_ids:
            continue
        seen_ids.add(bid)

        authors: list[str] = []
        for a in li.css("a[href^='/a/']"):
            href = a.attributes.get("href", "")
            if href == "/a/all":
                continue
            name = _node_text(a)
            if name and name not in authors:
                authors.append(name)

        res.books.append(
            SearchResultBook(id=bid, title=title, authors=authors, url=f"/b/{bid}")
        )

    # Pagination links.
    for a in tree.css("a"):
        text = _node_text(a).lower()
        href = a.attributes.get("href", "")
        if "page=" not in href:
            continue
        if "следующ" in text or text in ("next", ">", "›"):
            res.has_next = True
        if "предыдущ" in text or text in ("prev", "<", "‹"):
            res.has_prev = True

    if res.total == 0:
        res.total = len(res.books)

    return res


@dataclass
class BookInfo:
    id: int
    title: str
    authors: list[str] = field(default_factory=list)
    genres: list[str] = field(default_factory=list)
    annotation: str = ""
    cover_url: str = ""
    file_rating: Optional[int] = None  # 1..5 or None
    file_rating_raw: str = ""  # "файл не оценен" etc
    size: str = ""  # "14146K"
    pages: Optional[int] = None
    book_rating_avg: Optional[float] = None
    book_rating_count: Optional[int] = None
    formats: list[str] = field(default_factory=list)  # FB2, EPUB, MOBI
    added: str = ""
    has_fb2: bool = False
    has_epub: bool = False
    has_mobi: bool = False
    source_url: str = ""


def _parse_file_rating_alt(alt: str) -> Optional[int]:
    alt = (alt or "").lower().strip()
    if "не оценен" in alt:
        return None
    m = re.search(r"на\s*(\d)", alt)
    if m:
        return int(m.group(1))
    m = re.search(r"(\d)", alt)
    if m:
        return int(m.group(1))
    return None


def _parse_authors_and_genres(tree: HTMLParser, h1) -> tuple[list[str], list[str]]:
    """Read author/genre links from the book header only.

    Walking siblings of the title avoids the sidebar with recent comments,
    which contains many unrelated ``/a/`` links on real Flibusta pages.
    """
    authors: list[str] = []
    genres: list[str] = []
    if h1 is None:
        return authors, genres

    node = h1.next
    while node is not None:
        if node.tag == "h2" and "Аннотация" in _node_text(node):
            break
        if node.tag in ("h2", "hr"):
            break

        if node.tag == "a":
            links = [node]
        elif node.tag in ("div", "p", "span", "li"):
            links = node.css("a")
        else:
            links = []

        for a in links:
            href = a.attributes.get("href", "")
            text = _node_text(a)
            if not text:
                continue
            if href.startswith("/a/") and href != "/a/all":
                if text not in authors:
                    authors.append(text)
            elif href.startswith("/g/"):
                if text not in genres:
                    genres.append(text)
        node = node.next

    return authors, genres


def _parse_annotation(tree: HTMLParser) -> str:
    """Collect paragraphs after the "Аннотация" heading.

    Iterating only siblings prevents nested ``div``/``p`` text from being
    appended several times.  ``separator="\\n"`` preserves spaces around
    ``<br>`` and inline tags instead of gluing words together.
    """
    for h2 in tree.css("h2"):
        if not _node_text(h2).startswith("Аннотация"):
            continue
        parts: list[str] = []
        node = h2.next
        while node is not None:
            if node.tag in ("h2", "hr"):
                break
            if node.tag in ("p", "div"):
                text = node.text(separator="\n", strip=True)
                if text:
                    parts.append(text)
            node = node.next
        return "\n".join(parts).strip()[:2000]
    return ""


def parse_book(html: str, book_id: int, base_url: str = BASE) -> BookInfo:
    tree = HTMLParser(html)
    info = BookInfo(
        id=book_id,
        title="",
        source_url=f"{base_url.rstrip('/')}/b/{book_id}",
    )

    h1 = tree.css_first("h1.title")
    if h1 is not None:
        title = _node_text(h1)
        # Strip trailing " (fb2)" / " (epub)" / " (mobi)".
        title = re.sub(r"\s*\((fb2|epub|mobi)\)\s*$", "", title).strip()
        info.title = title

    info.authors, info.genres = _parse_authors_and_genres(tree, h1)

    # File rating image: img with src /img/znak*.gif.
    for img in tree.css("img"):
        src = img.attributes.get("src", "")
        if "/img/znak" in src:
            alt = img.attributes.get("alt", "") or img.attributes.get("title", "")
            info.file_rating_raw = alt
            info.file_rating = _parse_file_rating_alt(alt)
            break

    # Scope textual metadata to the main content block.  ``tree.body.text()``
    # also contains the sidebar with recent comments, which can produce false
    # matches for size/rating/date regexes.
    content = tree.css_first("#main") or tree.body
    body_text = content.text(separator="\n") if content is not None else html

    # Size and pages: text like "14146K, 1545 с." or "116K, 53 с.".
    m = re.search(r"(\d+(?:\.\d+)?[KMkm])\s*,\s*(\d+)\s*с\.", body_text)
    if m:
        info.size = m.group(1)
        info.pages = int(m.group(2))

    # Formats: links /b/{id}/fb2 etc.
    for a in tree.css("a"):
        href = a.attributes.get("href", "")
        if f"/b/{book_id}/fb2" in href:
            info.has_fb2 = True
            if "FB2" not in info.formats:
                info.formats.append("FB2")
        if f"/b/{book_id}/epub" in href:
            info.has_epub = True
            if "EPUB" not in info.formats:
                info.formats.append("EPUB")
        if f"/b/{book_id}/mobi" in href:
            info.has_mobi = True
            if "MOBI" not in info.formats:
                info.formats.append("MOBI")

    info.annotation = _parse_annotation(tree)

    # Cover: prefer an image whose alt mentions "cover", then one with the
    # book id in the path, then the first /i/ image in the main content.
    images: list[tuple[str, str]] = []
    image_scope = content.css("img") if content is not None else tree.css("img")
    for img in image_scope:
        src = img.attributes.get("src", "")
        if "/i/" in src:
            images.append((src, img.attributes.get("alt", "") or ""))
    for src, alt in images:
        if "cover" in alt.lower():
            info.cover_url = _abs(src, base_url)
            break
    if not info.cover_url:
        for src, _alt in images:
            if str(book_id) in src:
                info.cover_url = _abs(src, base_url)
                break
    if not info.cover_url and images:
        info.cover_url = _abs(images[0][0], base_url)

    # Book rating: "Оценки: 4, от 5 до 1, среднее  3.8".
    m = re.search(
        r"Оценки:\s*(\d+)\s*,\s*от\s*\d+\s*до\s*\d+\s*,\s*среднее\s*([\d.]+)",
        body_text,
    )
    if m:
        info.book_rating_count = int(m.group(1))
        info.book_rating_avg = float(m.group(2))

    # Added date.
    m = re.search(r"Добавлена:\s*([0-9.]+)", body_text)
    if m:
        info.added = m.group(1)

    return info
