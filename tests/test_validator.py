"""Validator tests."""

import zipfile
from pathlib import Path

from knigojor.downloader.converter import convert_fb2_to_epub
from knigojor.downloader.validator import validate_epub

FIX = Path(__file__).parent / "fixtures"


def test_validate_generated_epub(tmp_path):
    out = tmp_path / "out.epub"
    convert_fb2_to_epub(FIX / "book_sample.fb2", out)
    assert validate_epub(out) is True


def test_validate_missing_file(tmp_path):
    assert validate_epub(tmp_path / "nope.epub") is False


def test_validate_not_a_zip(tmp_path):
    path = tmp_path / "broken.epub"
    path.write_bytes(b"not an epub")
    assert validate_epub(path) is False


def test_validate_missing_manifest_resource(tmp_path):
    valid = tmp_path / "valid.epub"
    convert_fb2_to_epub(FIX / "book_sample.fb2", valid)

    broken = tmp_path / "broken.epub"
    with zipfile.ZipFile(valid) as source, zipfile.ZipFile(broken, "w") as target:
        for info in source.infolist():
            if info.filename == "EPUB/chap_1.xhtml":
                continue
            target.writestr(info, source.read(info.filename))

    assert validate_epub(broken) is False
