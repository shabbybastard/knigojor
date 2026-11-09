"""Search handlers."""

from __future__ import annotations

import logging

from aiogram import F, Router
from aiogram.types import CallbackQuery, Message

from ..formatting.html import esc
from ..keyboards.inline import main_menu_kb, results_kb, search_prompt_kb
from ..ui.message import MessageManager
from ..views import load_search_page, render_search_page

log = logging.getLogger("knigojor.handlers.search")


def register(router: Router, ctx) -> None:
    mm: MessageManager = ctx.message_manager

    async def do_search(chat_id, user_id, query, ui_page: int = 0):
        state = await ctx.state_store.get(user_id)
        state.chat_id = chat_id
        state.screen = "searching"
        await ctx.state_store.save(state)

        text = (
            f"<b>FLIBUSTA / SEARCH</b>\n\n"
            f"Поиск: {esc(query[:200])}\n\nЗагрузка..."
        )
        state.message_id = await mm.send_or_edit(
            ctx.bot, chat_id, state.message_id, text, None
        )
        await ctx.state_store.save(state)

        try:
            page = await load_search_page(
                ctx.client,
                query,
                ui_page,
                ctx.settings.page_size,
            )
        except Exception as exc:
            log.exception("search failed: %s", exc)
            text = (
                f"<b>FLIBUSTA / SEARCH</b>\n\n"
                f"Ошибка поиска: {esc(str(exc)[:300])}\n\n"
                f"Запрос: {esc(query[:200])}"
            )
            state.message_id = await mm.send_or_edit(
                ctx.bot, chat_id, state.message_id, text, main_menu_kb()
            )
            state.screen = "main"
            await ctx.state_store.save(state)
            return

        state.query = query
        state.page = page.ui_page
        state.screen = "results"
        state.extra = {
            "total": page.total,
            "total_pages": page.total_pages,
        }
        await ctx.state_store.save(state)

        text, visible_books = render_search_page(page, query)
        keyboard = results_kb(
            page.ui_page,
            page.total_pages,
            [book.id for book in visible_books],
        )
        state.message_id = await mm.send_or_edit(
            ctx.bot, chat_id, state.message_id, text, keyboard
        )
        await ctx.state_store.save(state)

    @router.callback_query(F.data == "new_search")
    async def cb_new_search(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        await cb.answer()
        async with mm.lock(cb.from_user.id):
            state = await ctx.state_store.get(cb.from_user.id)
            chat_id = cb.message.chat.id if cb.message else state.chat_id
            if chat_id is None:
                return
            state.chat_id = chat_id
            state.screen = "awaiting_search"
            state.message_id = state.message_id or (
                cb.message.message_id if cb.message else None
            )
            text = (
                "<b>FLIBUSTA / SEARCH</b>\n\n"
                "Введите название или автора отдельным сообщением."
            )
            state.message_id = await mm.send_or_edit(
                cb.bot, chat_id, state.message_id, text, search_prompt_kb()
            )
            await ctx.state_store.save(state)

    @router.callback_query(F.data == "cancel_search")
    async def cb_cancel(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        await cb.answer()
        async with mm.lock(cb.from_user.id):
            state = await ctx.state_store.get(cb.from_user.id)
            chat_id = cb.message.chat.id if cb.message else state.chat_id
            if chat_id is None:
                return
            state.chat_id = chat_id
            state.screen = "main"
            text = "<b>FLIBUSTA / knigojor</b>\n\nОтменено."
            state.message_id = await mm.send_or_edit(
                cb.bot, chat_id, state.message_id, text, main_menu_kb()
            )
            await ctx.state_store.save(state)

    @router.callback_query(F.data.startswith("page:"))
    async def cb_page(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        parts = cb.data.split(":")
        direction = parts[1] if len(parts) > 1 else ""
        expected_page = None
        if len(parts) > 2:
            try:
                expected_page = int(parts[2])
            except ValueError:
                expected_page = None

        state = await ctx.state_store.get(cb.from_user.id)
        if state.screen != "results" or not state.query:
            await cb.answer("Нет активного поиска", show_alert=True)
            return
        await cb.answer()

        async with mm.lock(cb.from_user.id):
            state = await ctx.state_store.get(cb.from_user.id)
            if state.screen != "results" or not state.query:
                return
            if expected_page is not None and state.page != expected_page:
                return
            chat_id = cb.message.chat.id if cb.message else state.chat_id
            if chat_id is None:
                return
            new_page = state.page
            if direction == "next":
                new_page += 1
            elif direction == "prev":
                new_page = max(0, new_page - 1)
            await do_search(
                chat_id,
                cb.from_user.id,
                state.query,
                new_page,
            )

    @router.message(F.text)
    async def on_text(message: Message):
        if message.from_user.id != ctx.settings.allowed_user_id:
            return
        async with mm.lock(message.from_user.id):
            state = await ctx.state_store.get(message.from_user.id)
            if state.screen != "awaiting_search":
                return

            query = message.text.strip()
            try:
                await message.delete()
            except Exception:
                pass

            if not query:
                text = (
                    "<b>FLIBUSTA / SEARCH</b>\n\n"
                    "Введите непустой запрос."
                )
                state.message_id = await mm.send_or_edit(
                    message.bot,
                    message.chat.id,
                    state.message_id,
                    text,
                    search_prompt_kb(),
                )
                await ctx.state_store.save(state)
                return

            state.screen = "main"
            await ctx.state_store.save(state)
            await do_search(message.chat.id, message.from_user.id, query, 0)
