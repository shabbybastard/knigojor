"""Search pagination and rendering tests."""

from knigojor.bot.views import (
    SearchPage,
    load_search_page,
    render_search_page,
    render_search_results,
)
from knigojor.flibusta.parser import SearchResultBook, SearchResults


class FakeClient:
    def __init__(self):
        self.calls: list[int] = []

    async def search(self, query: str, page: int) -> SearchResults:
        self.calls.append(page)
        start = page * 50 + 1
        end = min(start + 50, 563)
        books = [
            SearchResultBook(
                id=book_id,
                title=f"Книга {book_id}",
                authors=[f"Автор {book_id}"],
                url=f"/b/{book_id}",
            )
            for book_id in range(start, end)
        ]
        return SearchResults(
            books=books,
            total=562,
            page=page,
            has_next=end < 562,
            has_prev=page > 0,
        )


async def test_ui_page_is_sliced_from_server_page():
    client = FakeClient()
    page = await load_search_page(client, "query", 0, page_size=5)

    assert page.total == 562
    assert page.total_pages == 113
    assert page.start_index == 1
    assert [book.id for book in page.books] == [1, 2, 3, 4, 5]
    assert client.calls == [0]


async def test_page_crosses_to_next_server_page():
    client = FakeClient()
    page = await load_search_page(client, "query", 10, page_size=5)

    assert page.start_index == 51
    assert [book.id for book in page.books] == [51, 52, 53, 54, 55]
    assert client.calls == [1]


async def test_last_page_is_short():
    client = FakeClient()
    page = await load_search_page(client, "query", 112, page_size=5)

    assert page.ui_page == 112
    assert page.start_index == 561
    assert [book.id for book in page.books] == [561, 562]
    assert client.calls == [11]


async def test_rendered_message_fits_telegram_limit():
    client = FakeClient()
    page = await load_search_page(client, "query", 0, page_size=5)
    text = render_search_results(page, "query")

    assert len(text) < 4096
    assert "Страница 1 из 113" in text
    assert "Книга 1" in text


async def test_page_size_30_crosses_server_boundary():
    client = FakeClient()
    page = await load_search_page(client, "query", 1, page_size=30)

    assert page.start_index == 31
    assert [book.id for book in page.books] == list(range(31, 61))
    assert client.calls == [0, 1]


def test_rendering_drops_books_instead_of_exceeding_limit():
    books = [
        SearchResultBook(
            id=i,
            title="Очень длинное название " * 30,
            authors=["Очень длинный автор " * 10],
        )
        for i in range(1, 31)
    ]
    page = SearchPage(
        books=books,
        total=30,
        ui_page=0,
        total_pages=1,
        page_size=30,
    )
    text, visible = render_search_page(page, "query")

    assert len(text) <= 3900
    assert 0 < len(visible) < 30
