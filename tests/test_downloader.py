"""Downloader tests."""

import zipfile
from pathlib import Path

import pytest

from knigojor.downloader.download import download_fb2_zip


class FakeHttp:
    def __init__(self, archive: Path):
        self.archive = archive

    async def download(self, url, dest, max_bytes=None, timeout=None, **kwargs):
        data = self.archive.read_bytes()
        if max_bytes is not None and len(data) > max_bytes:
            raise ValueError("File too large")
        Path(dest).write_bytes(data)
        return len(data)


async def test_download_fb2_extracts_nested_file(tmp_path):
    archive_path = tmp_path / "book.zip"
    with zipfile.ZipFile(archive_path, "w") as archive:
        archive.writestr("dir/book.fb2", b"<FictionBook/>")

    result = await download_fb2_zip(
        FakeHttp(archive_path),
        "http://example/book",
        tmp_path / "out",
        max_size_mb=1,
    )

    assert result == tmp_path / "out" / "dir" / "book.fb2"
    assert result.exists()
    assert not (tmp_path / "out" / "book.fb2.zip").exists()


async def test_download_fb2_rejects_zip_bomb(tmp_path):
    archive_path = tmp_path / "bomb.zip"
    with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("book.fb2", b"0" * (2 * 1024 * 1024))

    with pytest.raises(ValueError, match="too large"):
        await download_fb2_zip(
            FakeHttp(archive_path),
            "http://example/book",
            tmp_path / "out",
            max_size_mb=1,
        )
