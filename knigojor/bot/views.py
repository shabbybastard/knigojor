"""Shared rendering and pagination helpers for the bot UI."""

from __future__ import annotations

import re
from dataclasses import dataclass

from ..flibusta.parser import BookInfo, SearchResultBook
from .formatting.html import esc

# Flibusta returns up to 50 books per search page.  The bot slices that
# server page into smaller UI pages controlled by PAGE_SIZE.
SERVER_PAGE_SIZE = 50


def _clip(value, limit: int) -> str:
    text = "" if value is None else str(value)
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 1)].rstrip() + "…"


@dataclass
class SearchPage:
    books: list[SearchResultBook]
    total: int
    ui_page: int
    total_pages: int
    page_size: int

    @property
    def start_index(self) -> int:
        return self.ui_page * self.page_size + 1

    @property
    def has_prev(self) -> bool:
        return self.ui_page > 0

    @property
    def has_next(self) -> bool:
        return self.ui_page < self.total_pages - 1


async def load_search_page(client, query: str, ui_page: int, page_size: int = 5) -> SearchPage:
    """Load one UI page from Flibusta's 50-book server pages.

    A UI page is normally a slice of one server page, but it can cross a
    server-page boundary when ``PAGE_SIZE`` is large.  Fetch all server pages
    needed for the requested slice.
    """
    page_size = max(1, min(int(page_size), SERVER_PAGE_SIZE))
    ui_page = max(0, int(ui_page))

    async def fetch(server_page: int):
        return await client.search(query, server_page)

    def server_page_for(page: int) -> int:
        return (page * page_size) // SERVER_PAGE_SIZE

    results = await fetch(server_page_for(ui_page))
    total = results.total or len(results.books)
    total_pages = max(1, (total + page_size - 1) // page_size)

    # Clamp stale UI state after the result set has changed.
    if ui_page >= total_pages:
        ui_page = total_pages - 1
        results = await fetch(server_page_for(ui_page))
        total = results.total or len(results.books)
        total_pages = max(1, (total + page_size - 1) // page_size)

    start = ui_page * page_size
    first_server = start // SERVER_PAGE_SIZE
    last_server = (start + page_size - 1) // SERVER_PAGE_SIZE
    offset = start % SERVER_PAGE_SIZE

    books: list[SearchResultBook] = []
    for server_page in range(first_server, last_server + 1):
        page_results = (
            results if server_page == first_server else await fetch(server_page)
        )
        if server_page == first_server:
            books.extend(page_results.books[offset:])
        else:
            books.extend(page_results.books)
    books = books[:page_size]

    return SearchPage(
        books=books,
        total=total,
        ui_page=ui_page,
        total_pages=total_pages,
        page_size=page_size,
    )


def render_search_page(
    page: SearchPage,
    query: str,
    max_length: int = 3900,
) -> tuple[str, list[SearchResultBook]]:
    """Render a page and drop trailing books if the text is too long.

    The returned list must be used to build the keyboard, otherwise buttons
    could point to books that are not visible in the message.
    """
    books = list(page.books)
    while books:
        candidate = SearchPage(
            books=books,
            total=page.total,
            ui_page=page.ui_page,
            total_pages=page.total_pages,
            page_size=page.page_size,
        )
        text = render_search_results(candidate, query)
        if len(text) <= max_length:
            return text, books
        books.pop()

    empty = SearchPage(
        books=[],
        total=page.total,
        ui_page=page.ui_page,
        total_pages=page.total_pages,
        page_size=page.page_size,
    )
    return render_search_results(empty, query), []


def render_search_results(page: SearchPage, query: str) -> str:
    lines = [
        "<b>FLIBUSTA / SEARCH</b>",
        "",
        f"Запрос: {esc(_clip(query, 200))}",
        f"Найдено: {page.total}",
        f"Страница {page.ui_page + 1} из {page.total_pages}",
        "",
    ]
    if not page.books:
        lines.append("Ничего не найдено.")
        return "\n".join(lines)

    for i, book in enumerate(page.books, start=page.start_index):
        lines.append(f"<b>{i}. {esc(_clip(book.title, 180))}</b>")
        if book.authors:
            lines.append(f"Автор: {esc(_clip(', '.join(book.authors), 180))}")
        lines.append("")
    return "\n".join(lines).rstrip()


def render_book(info: BookInfo) -> str:
    lines = [
        "<b>FLIBUSTA / BOOK</b>",
        "",
        f"<b>{esc(_clip(info.title, 300))}</b>",
    ]
    if info.authors:
        lines.append(f"Автор: {esc(_clip(', '.join(info.authors), 300))}")
    if info.genres:
        lines.append(f"Жанр: {esc(_clip(', '.join(info.genres), 300))}")
    if info.book_rating_avg is not None:
        rating = f"★ {info.book_rating_avg:.2f}"
        if info.book_rating_count:
            rating += f" ({info.book_rating_count})"
        lines.append(f"Рейтинг книги: {rating}")
    if info.file_rating is not None:
        lines.append(f"Рейтинг файла: {info.file_rating}")
    elif info.file_rating_raw:
        lines.append(f"Рейтинг файла: {esc(_clip(info.file_rating_raw, 100))}")
    if info.size:
        lines.append(f"Размер: {esc(_clip(info.size, 40))}")
    if info.pages:
        lines.append(f"Страниц: {info.pages}")
    if info.formats:
        lines.append("Форматы: " + ", ".join(info.formats))
    if info.annotation:
        lines.append("")
        lines.append(f"<blockquote>{esc(_clip(info.annotation, 1000))}</blockquote>")
    if info.cover_url:
        lines.append("")
        lines.append(f'<a href="{esc(info.cover_url, quote=True)}">Обложка</a>')
    source_url = info.source_url or f"https://flibusta.is/b/{info.id}"
    lines.append("")
    lines.append(f'<a href="{esc(source_url, quote=True)}">Оригинальная страница</a>')
    return "\n".join(lines)


def filename_for(title: str, book_id: int) -> str:
    safe = re.sub(r'[\\/:*?"<>|\x00-\x1f]', "_", title or "").strip().strip(".")
    safe = _clip(safe, 100)
    return f"{safe or book_id}.epub"
