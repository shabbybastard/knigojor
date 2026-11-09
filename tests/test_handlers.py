"""Handler-level tests with fake Telegram/Flibusta objects."""

from __future__ import annotations

from pathlib import Path
from types import SimpleNamespace

from aiogram import Router

from knigojor.bot.handlers import book as book_h
from knigojor.bot.handlers import download as download_h
from knigojor.bot.handlers import search as search_h
from knigojor.bot.ui.message import MessageManager
from knigojor.flibusta.parser import BookInfo, SearchResultBook, SearchResults
from knigojor.storage.state import UIState


class FakeBot:
    def __init__(self):
        self.messages = []
        self.documents = []

    async def edit_message_text(self, **kwargs):
        self.messages.append(kwargs)
        return True

    async def send_message(self, **kwargs):
        self.messages.append(kwargs)
        return SimpleNamespace(message_id=len(self.messages))

    async def send_document(self, **kwargs):
        self.documents.append(kwargs)


class FakeStateStore:
    def __init__(self, state: UIState):
        self.state = state

    async def get(self, user_id):
        return self.state

    async def save(self, state):
        self.state = state


class FakeClient:
    def __init__(self):
        self.search_calls = []
        self.book_calls = []

    async def search(self, query, page):
        self.search_calls.append(page)
        start = page * 50 + 1
        end = min(start + 50, 563)
        return SearchResults(
            books=[
                SearchResultBook(id=i, title=f"Book {i}", authors=["Author"])
                for i in range(start, end)
            ],
            total=562,
            page=page,
            has_next=end < 562,
        )

    async def get_book(self, book_id):
        self.book_calls.append(book_id)
        return BookInfo(
            id=book_id,
            title="Book title",
            authors=["Author"],
            has_epub=True,
            has_fb2=False,
            source_url="https://mirror/b/1",
        )

    def http_for(self, mirror):
        return object()


class FakeMirrors:
    async def run_with_failover(self, operation):
        mirror = SimpleNamespace(url="https://mirror", is_onion=False)
        return await operation(mirror), mirror


class FakeContext:
    def __init__(self, tmp_path: Path, state: UIState):
        self.settings = SimpleNamespace(
            allowed_user_id=1,
            page_size=5,
            download_timeout=10,
            max_file_size_mb=5,
            temp_path=tmp_path,
        )
        self.message_manager = MessageManager()
        self.state_store = FakeStateStore(state)
        self.client = FakeClient()
        self.mirrors = FakeMirrors()
        self.bot = FakeBot()
        self.download_semaphore = None

    async def download_epub(self, http, url, dest, timeout=None):
        dest = Path(dest)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(b"epub")
        return dest

    async def download_fb2(self, http, url, tmpdir, timeout=None):
        path = Path(tmpdir) / "book.fb2"
        path.write_text("<FictionBook/>")
        return path

    def convert(self, source, dest):
        Path(dest).write_bytes(b"epub")


def _handler(router: Router, kind: str, name: str):
    observer = getattr(router, kind)
    for handler in observer.handlers:
        if getattr(handler.callback, "__name__", "") == name:
            return handler.callback
    raise AssertionError(f"handler {name} not found")


def _callback(data: str, bot: FakeBot, *, user_id: int = 1):
    async def answer(*args, **kwargs):
        return None

    return SimpleNamespace(
        from_user=SimpleNamespace(id=user_id),
        data=data,
        message=SimpleNamespace(chat=SimpleNamespace(id=10)),
        bot=bot,
        answer=answer,
    )


def _message(text: str, bot: FakeBot):
    async def delete():
        return None

    return SimpleNamespace(
        from_user=SimpleNamespace(id=1),
        chat=SimpleNamespace(id=10),
        bot=bot,
        text=text,
        delete=delete,
    )


async def test_search_text_handler(tmp_path):
    state = UIState(user_id=1, chat_id=10, message_id=5, screen="awaiting_search")
    ctx = FakeContext(tmp_path, state)
    router = Router()
    search_h.register(router, ctx)

    await _handler(router, "message", "on_text")(_message("Пушкин", ctx.bot))

    assert ctx.state_store.state.screen == "results"
    assert ctx.state_store.state.query == "Пушкин"
    assert ctx.state_store.state.page == 0
    assert ctx.client.search_calls == [0]
    assert "Страница 1 из 113" in ctx.bot.messages[-1]["text"]


async def test_stale_page_callback_is_ignored(tmp_path):
    state = UIState(user_id=1, chat_id=10, message_id=5, screen="results", query="q", page=0)
    ctx = FakeContext(tmp_path, state)
    router = Router()
    search_h.register(router, ctx)
    handler = _handler(router, "callback_query", "cb_page")

    await handler(_callback("page:next:5", ctx.bot))
    assert ctx.client.search_calls == []
    assert ctx.state_store.state.page == 0

    await handler(_callback("page:next:0", ctx.bot))
    assert ctx.client.search_calls == [0]
    assert ctx.state_store.state.page == 1


async def test_book_card_uses_real_format_flags(tmp_path):
    state = UIState(user_id=1, chat_id=10, message_id=5, screen="results")
    ctx = FakeContext(tmp_path, state)
    router = Router()
    book_h.register(router, ctx)

    await _handler(router, "callback_query", "cb_book")(
        _callback("book:1:1", ctx.bot)
    )

    assert ctx.state_store.state.screen == "book"
    markup = ctx.bot.messages[-1]["reply_markup"]
    buttons = [button for row in markup.inline_keyboard for button in row]
    assert any(button.callback_data == "dl:1" for button in buttons)


async def test_download_handler_success(tmp_path, monkeypatch):
    state = UIState(user_id=1, chat_id=10, message_id=5, screen="book")
    ctx = FakeContext(tmp_path, state)
    router = Router()
    download_h.register(router, ctx)
    monkeypatch.setattr(download_h, "validate_epub", lambda path: True)

    await _handler(router, "callback_query", "cb_download")(
        _callback("dl:1", ctx.bot)
    )

    assert len(ctx.bot.documents) == 1
    assert ctx.state_store.state.screen == "book"
    assert "Книга отправлена" in ctx.bot.messages[-1]["text"]
