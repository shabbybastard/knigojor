"""File download helpers."""

from __future__ import annotations


def fb2_url(mirror_url: str, book_id: int) -> str:
    return mirror_url.rstrip("/") + f"/b/{book_id}/fb2"


def epub_url(mirror_url: str, book_id: int) -> str:
    return mirror_url.rstrip("/") + f"/b/{book_id}/epub"
