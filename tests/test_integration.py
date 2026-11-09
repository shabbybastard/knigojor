"""End-to-end tests against a local Flibusta-like HTTP server."""

from __future__ import annotations

import asyncio
import io
import zipfile
from pathlib import Path

import pytest
from aiohttp import ClientResponseError, web

from knigojor.bot.views import load_search_page
from knigojor.downloader.converter import convert_fb2_to_epub
from knigojor.downloader.download import download_epub, download_fb2_zip
from knigojor.downloader.validator import validate_epub
from knigojor.flibusta.client import FlibustaClient
from knigojor.network.healthcheck import check_mirror
from knigojor.network.http import HttpClient
from knigojor.network.mirrors import MirrorManager

FIX = Path(__file__).parent / "fixtures"


@pytest.fixture
async def flibusta_server(tmp_path):
    search_html = (FIX / "search_pushkin.html").read_text(encoding="utf-8")
    book_html = (FIX / "book_820555.html").read_text(encoding="utf-8")
    epub_path = tmp_path / "source.epub"
    convert_fb2_to_epub(FIX / "book_sample.fb2", epub_path)
    epub_bytes = epub_path.read_bytes()

    fb2_zip = io.BytesIO()
    with zipfile.ZipFile(fb2_zip, "w") as archive:
        archive.writestr("book.fb2", (FIX / "book_sample.fb2").read_text(encoding="utf-8"))

    retry_count = {"value": 0}

    async def search(request):
        return web.Response(text=search_html, content_type="text/html", charset="utf-8")

    async def book(request):
        return web.Response(text=book_html, content_type="text/html", charset="utf-8")

    async def epub(request):
        return web.Response(body=epub_bytes, content_type="application/epub+zip")

    async def fb2(request):
        return web.Response(body=fb2_zip.getvalue(), content_type="application/zip")

    async def retry(request):
        retry_count["value"] += 1
        if retry_count["value"] == 1:
            return web.Response(status=500, text="temporary")
        return web.Response(text="ok")

    async def not_found(request):
        retry_count["not_found"] = retry_count.get("not_found", 0) + 1
        return web.Response(status=404, text="missing")

    async def root(request):
        return web.Response(text="ok")

    app = web.Application()
    app.router.add_get("/", root)
    app.router.add_get("/booksearch", search)
    app.router.add_get("/b/820555", book)
    app.router.add_get("/b/820555/epub", epub)
    app.router.add_get("/b/820555/fb2", fb2)
    app.router.add_get("/retry", retry)
    app.router.add_get("/not-found", not_found)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, "127.0.0.1", 0)
    await site.start()
    port = site._server.sockets[0].getsockname()[1]
    try:
        yield f"http://127.0.0.1:{port}", retry_count
    finally:
        await runner.cleanup()


async def test_search_and_book_with_mirror_failover(flibusta_server):
    base_url, _ = flibusta_server
    http = HttpClient(timeout=5, max_retries=1)
    await http.start()
    try:
        mirrors = MirrorManager(
            ["http://127.0.0.1:1", base_url],
            tor_enabled=False,
        )
        client = FlibustaClient(mirrors, http)

        results = await client.search("Пушкин", 0)
        assert len(results.books) == 50
        assert results.total == 562
        assert mirrors.mirrors[0].failures == 1
        assert mirrors.mirrors[1].failures == 0

        page = await load_search_page(client, "Пушкин", 0, page_size=5)
        assert len(page.books) == 5
        assert page.total_pages == 113

        info = await client.get_book(820555)
        assert info.title == "Война и мир"
        assert info.authors == ["Лев Николаевич Толстой"]
        assert info.source_url.startswith(base_url)
    finally:
        await http.close()


async def test_download_and_convert_epub(flibusta_server, tmp_path):
    base_url, _ = flibusta_server
    http = HttpClient(timeout=5, max_retries=0)
    await http.start()
    try:
        epub_path = await download_epub(
            http,
            f"{base_url}/b/820555/epub",
            tmp_path / "downloaded.epub",
            max_size_mb=5,
        )
        assert validate_epub(epub_path)

        fb2_path = await download_fb2_zip(
            http,
            f"{base_url}/b/820555/fb2",
            tmp_path / "fb2",
            max_size_mb=5,
        )
        converted = await asyncio.to_thread(
            convert_fb2_to_epub,
            fb2_path,
            tmp_path / "converted.epub",
        )
        assert validate_epub(converted)
    finally:
        await http.close()


async def test_http_retries_on_5xx(flibusta_server):
    base_url, retry_count = flibusta_server
    http = HttpClient(timeout=5, max_retries=1)
    await http.start()
    try:
        assert await http.get_text(f"{base_url}/retry") == "ok"
        assert retry_count["value"] == 2
    finally:
        await http.close()


async def test_healthcheck_against_local_server(flibusta_server):
    base_url, _ = flibusta_server
    http = HttpClient(timeout=5, max_retries=0)
    await http.start()
    try:
        assert await check_mirror(http, base_url) is True
        assert await check_mirror(http, "http://127.0.0.1:1") is False
    finally:
        await http.close()


async def test_http_does_not_retry_4xx(flibusta_server):
    base_url, retry_count = flibusta_server
    http = HttpClient(timeout=5, max_retries=2)
    await http.start()
    try:
        with pytest.raises(ClientResponseError):
            await http.get_text(f"{base_url}/not-found")
        assert retry_count["not_found"] == 1
    finally:
        await http.close()
