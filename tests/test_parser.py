"""Parser tests."""

from pathlib import Path

from knigojor.flibusta import parser as P

FIX = Path(__file__).parent / "fixtures"


def test_parse_book_title():
    html = (FIX / "book_820555.html").read_text(encoding="utf-8")
    info = P.parse_book(html, 820555)
    assert "Война и мир" in info.title
    assert info.authors == ["Лев Николаевич Толстой"]
    assert "Классическая проза ХIX века" in info.genres


def test_parse_book_size_pages():
    html = (FIX / "book_820555.html").read_text(encoding="utf-8")
    info = P.parse_book(html, 820555)
    assert info.size == "14146K"
    assert info.pages == 1545


def test_parse_book_rating():
    html = (FIX / "book_820555.html").read_text(encoding="utf-8")
    info = P.parse_book(html, 820555)
    assert info.book_rating_count == 4
    assert abs(info.book_rating_avg - 3.8) < 0.01


def test_parse_book_formats():
    html = (FIX / "book_820555.html").read_text(encoding="utf-8")
    info = P.parse_book(html, 820555)
    assert "FB2" in info.formats
    assert "EPUB" in info.formats
    assert info.has_fb2 and info.has_epub


def test_parse_book_annotation_and_source():
    html = (FIX / "book_820555.html").read_text(encoding="utf-8")
    info = P.parse_book(html, 820555, base_url="https://mirror.example")
    assert info.annotation
    assert "книге.В" not in info.annotation
    assert "В минуты отъезда" in info.annotation
    assert info.cover_url.startswith("https://mirror.example/")
    assert info.source_url == "https://mirror.example/b/820555"


def test_parse_search():
    html = (FIX / "search_pushkin.html").read_text(encoding="utf-8")
    res = P.parse_search(html, page=0)
    assert len(res.books) == 50
    assert res.total == 562
    assert res.has_next
    assert not res.has_prev
    book = res.books[0]
    assert book.title
    assert book.id == 552916
    assert book.authors == ["Борис Степанович Житков"]
