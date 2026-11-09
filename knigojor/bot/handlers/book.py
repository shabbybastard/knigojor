"""Book card handlers."""

from __future__ import annotations

import logging

from aiogram import F, Router
from aiogram.types import CallbackQuery

from ..formatting.html import esc
from ..keyboards.inline import book_kb, main_menu_kb, results_kb
from ..ui.message import MessageManager
from ..views import load_search_page, render_book, render_search_page

log = logging.getLogger("knigojor.handlers.book")


def register(router: Router, ctx) -> None:
    mm: MessageManager = ctx.message_manager

    @router.callback_query(F.data.startswith("book:"))
    async def cb_book(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        await cb.answer()

        parts = cb.data.split(":")
        try:
            book_id = int(parts[1])
        except (IndexError, ValueError):
            return

        async with mm.lock(cb.from_user.id):
            state = await ctx.state_store.get(cb.from_user.id)
            chat_id = cb.message.chat.id if cb.message else state.chat_id
            if chat_id is None:
                return
            try:
                info = await ctx.client.get_book(book_id)
            except Exception as exc:
                log.exception("book fetch failed: %s", exc)
                text = (
                    "<b>FLIBUSTA / BOOK</b>\n\n"
                    f"Ошибка загрузки книги: {esc(str(exc)[:300])}"
                )
                state.message_id = await mm.send_or_edit(
                    cb.bot, chat_id, state.message_id, text, main_menu_kb()
                )
                state.screen = "main"
                await ctx.state_store.save(state)
                return

            state.screen = "book"
            state.book_id = book_id
            text = render_book(info)
            keyboard = book_kb(book_id, info.has_epub, info.has_fb2)
            state.message_id = await mm.send_or_edit(
                cb.bot, chat_id, state.message_id, text, keyboard
            )
            await ctx.state_store.save(state)

    @router.callback_query(F.data == "back_results")
    async def cb_back(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        await cb.answer()

        async with mm.lock(cb.from_user.id):
            state = await ctx.state_store.get(cb.from_user.id)
            chat_id = cb.message.chat.id if cb.message else state.chat_id
            if chat_id is None:
                return

            if not state.query:
                state.screen = "main"
                text = "<b>FLIBUSTA / knigojor</b>\n\nПерсональная электронная библиотека."
                state.message_id = await mm.send_or_edit(
                    cb.bot, chat_id, state.message_id, text, main_menu_kb()
                )
                await ctx.state_store.save(state)
                return

            try:
                page = await load_search_page(
                    ctx.client,
                    state.query,
                    state.page,
                    ctx.settings.page_size,
                )
            except Exception as exc:
                log.exception("re-search failed: %s", exc)
                text = (
                    "<b>FLIBUSTA / SEARCH</b>\n\n"
                    f"Ошибка загрузки результатов: {esc(str(exc)[:300])}"
                )
                state.message_id = await mm.send_or_edit(
                    cb.bot, chat_id, state.message_id, text, main_menu_kb()
                )
                state.screen = "main"
                await ctx.state_store.save(state)
                return

            state.screen = "results"
            state.page = page.ui_page
            state.extra = {
                "total": page.total,
                "total_pages": page.total_pages,
            }
            text, visible_books = render_search_page(page, state.query)
            keyboard = results_kb(
                page.ui_page,
                page.total_pages,
                [book.id for book in visible_books],
            )
            state.message_id = await mm.send_or_edit(
                cb.bot, chat_id, state.message_id, text, keyboard
            )
            await ctx.state_store.save(state)
