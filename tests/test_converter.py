"""FB2 -> EPUB converter tests."""

import zipfile
from pathlib import Path

from knigojor.downloader.converter import convert_fb2_to_epub
from knigojor.downloader.validator import validate_epub

FIX = Path(__file__).parent / "fixtures"


def test_converter_builds_valid_epub(tmp_path):
    out = tmp_path / "book.epub"
    convert_fb2_to_epub(FIX / "book_sample.fb2", out)

    assert validate_epub(out)

    with zipfile.ZipFile(out) as archive:
        names = archive.namelist()
        assert len(names) == len(set(names))
        assert names.count("EPUB/cover.png") == 1

        chapter = archive.read("EPUB/chap_1.xhtml").decode("utf-8")
        assert "<h2>Глава 1</h2>" in chapter
        assert "<p>Глава 1</p>" not in chapter
        assert "Первый абзац." in chapter

        opf = archive.read("EPUB/content.opf").decode("utf-8")
        assert "Краткая аннотация" in opf
