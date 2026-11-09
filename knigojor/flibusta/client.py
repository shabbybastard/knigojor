"""Flibusta HTTP client integrating mirrors and parsing."""

from __future__ import annotations

import logging
import urllib.parse
from typing import Optional

from ..network.http import HttpClient
from ..network.mirrors import Mirror, MirrorManager
from .parser import BookInfo, SearchResults, parse_book, parse_search

log = logging.getLogger("knigojor.flibusta")


class FlibustaClient:
    def __init__(
        self,
        mirror_manager: MirrorManager,
        http_direct: HttpClient,
        http_tor: Optional[HttpClient] = None,
    ):
        self.mirrors = mirror_manager
        self.http_direct = http_direct
        self.http_tor = http_tor

    def http_for(self, mirror: Mirror) -> HttpClient:
        if mirror.is_onion:
            if self.http_tor is None:
                raise RuntimeError("Tor is not available for onion mirror")
            return self.http_tor
        return self.http_direct

    async def search(self, query: str, page: int = 0) -> SearchResults:
        q = urllib.parse.quote(query, safe="")
        path = f"/booksearch?ask={q}&page={page}"

        async def op(mirror: Mirror):
            http = self.http_for(mirror)
            text = await http.get_text(mirror.url + path)
            return parse_search(text, page=page)

        result, _ = await self.mirrors.run_with_failover(op)
        return result

    async def get_book(self, book_id: int) -> BookInfo:
        path = f"/b/{book_id}"

        async def op(mirror: Mirror):
            http = self.http_for(mirror)
            text = await http.get_text(mirror.url + path)
            return parse_book(text, book_id, base_url=mirror.url)

        result, _ = await self.mirrors.run_with_failover(op)
        return result
