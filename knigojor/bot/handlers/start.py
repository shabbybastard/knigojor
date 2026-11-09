"""Start / main menu handlers."""

from __future__ import annotations

import logging

from aiogram import F, Router
from aiogram.filters import Command, CommandStart
from aiogram.types import CallbackQuery, Message

from ..keyboards.inline import main_menu_kb
from ..ui.message import MessageManager

log = logging.getLogger("knigojor.handlers.start")


def register(router: Router, ctx) -> None:
    mm: MessageManager = ctx.message_manager

    @router.message(Command("status"))
    async def cmd_status(message: Message):
        if message.from_user.id != ctx.settings.allowed_user_id:
            return
        async with mm.lock(message.from_user.id):
            state = await ctx.state_store.get(message.from_user.id)
            state.chat_id = message.chat.id
            text = await ctx.status_text()
            mid = await mm.send_or_edit(
                message.bot, message.chat.id, state.message_id, text, main_menu_kb()
            )
            state.message_id = mid
            state.screen = "status"
            await ctx.state_store.save(state)

    @router.message(CommandStart())
    async def cmd_start(message: Message):
        if message.from_user.id != ctx.settings.allowed_user_id:
            return
        async with mm.lock(message.from_user.id):
            state = await ctx.state_store.get(message.from_user.id)
            state.chat_id = message.chat.id
            state.screen = "main"
            await ctx.state_store.save(state)
            text = (
                "<b>FLIBUSTA / knigojor</b>\n\n"
                "Персональная электронная библиотека.\n\n"
                f"Состояние соединения: {ctx.connection_status()}"
            )
            mid = await mm.send_or_edit(
                message.bot, message.chat.id, state.message_id, text, main_menu_kb()
            )
            state.message_id = mid
            await ctx.state_store.save(state)

    @router.callback_query(F.data == "main_menu")
    async def cb_main_menu(cb: CallbackQuery):
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
            text = (
                "<b>FLIBUSTA / knigojor</b>\n\n"
                "Персональная электронная библиотека.\n\n"
                f"Состояние соединения: {ctx.connection_status()}"
            )
            mid = await mm.send_or_edit(
                cb.bot, chat_id, state.message_id, text, main_menu_kb()
            )
            state.message_id = mid
            await ctx.state_store.save(state)

    @router.callback_query(F.data == "refresh")
    async def cb_refresh(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        await cb_main_menu(cb)

    @router.callback_query(F.data == "status")
    async def cb_status(cb: CallbackQuery):
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
            text = await ctx.status_text()
            mid = await mm.send_or_edit(
                cb.bot, chat_id, state.message_id, text, main_menu_kb()
            )
            state.message_id = mid
            state.screen = "status"
            await ctx.state_store.save(state)
