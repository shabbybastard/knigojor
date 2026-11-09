"""Streaming download of books."""

from __future__ import annotations

import logging
import zipfile
from pathlib import Path

log = logging.getLogger("knigojor.download")


async def download_epub(
    http,
    url: str,
    dest: Path,
    max_size_mb: int = 50,
    timeout: int | float | None = None,
) -> Path:
    dest = Path(dest)
    dest.parent.mkdir(parents=True, exist_ok=True)
    max_bytes = max_size_mb * 1024 * 1024
    total = await http.download(
        url,
        dest,
        max_bytes=max_bytes,
        timeout=timeout,
    )
    if total == 0:
        dest.unlink(missing_ok=True)
        raise ValueError("Empty file")
    return dest


async def download_fb2_zip(
    http,
    url: str,
    dest_dir: Path,
    max_size_mb: int = 50,
    timeout: int | float | None = None,
) -> Path:
    """Download an FB2 archive and extract the first .fb2 file."""
    dest_dir = Path(dest_dir)
    dest_dir.mkdir(parents=True, exist_ok=True)
    max_bytes = max_size_mb * 1024 * 1024
    zip_path = dest_dir / "book.fb2.zip"

    try:
        total = await http.download(
            url,
            zip_path,
            max_bytes=max_bytes,
            timeout=timeout,
        )
        if total == 0:
            raise ValueError("Empty file")

        with zipfile.ZipFile(zip_path, "r") as archive:
            infos = archive.infolist()
            if len(infos) > 1000:
                raise ValueError("Archive contains too many files")
            uncompressed = sum(info.file_size for info in infos)
            if uncompressed > max_bytes:
                raise ValueError("Archive is too large after extraction")

            fb2_names = [name for name in archive.namelist() if name.lower().endswith(".fb2")]
            if not fb2_names:
                raise ValueError("No .fb2 in archive")
            name = fb2_names[0]
            archive.extract(name, dest_dir)
            return dest_dir / name
    finally:
        zip_path.unlink(missing_ok=True)
