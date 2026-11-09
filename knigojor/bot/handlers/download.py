"""Download handlers."""

from __future__ import annotations

import asyncio
import logging
import shutil
from pathlib import Path

from aiogram import F, Router
from aiogram.types import CallbackQuery, FSInputFile

from ...downloader.validator import validate_epub
from ...flibusta.files import epub_url, fb2_url
from ..formatting.html import esc
from ..keyboards.inline import book_kb, main_menu_kb
from ..ui.message import MessageManager
from ..views import filename_for, render_book

log = logging.getLogger("knigojor.handlers.download")


def register(router: Router, ctx) -> None:
    mm: MessageManager = ctx.message_manager

    @router.callback_query(F.data.startswith("dl:"))
    async def cb_download(cb: CallbackQuery):
        if cb.from_user.id != ctx.settings.allowed_user_id:
            await cb.answer("Доступ запрещён", show_alert=True)
            return
        await cb.answer()

        try:
            book_id = int(cb.data.split(":")[1])
        except (IndexError, ValueError):
            return

        async def run():
            async with mm.lock(cb.from_user.id):
                state = await ctx.state_store.get(cb.from_user.id)
                chat_id = cb.message.chat.id if cb.message else state.chat_id
                if chat_id is None:
                    return
                await _do_download(cb, chat_id, book_id, state)

        semaphore = getattr(ctx, "download_semaphore", None)
        if semaphore is None:
            await run()
        else:
            async with semaphore:
                await run()

    async def _do_download(cb, chat_id, book_id, state):
        async def status(text: str, keyboard=None):
            state.message_id = await mm.send_or_edit(
                cb.bot,
                chat_id,
                state.message_id,
                text,
                keyboard,
            )
            await ctx.state_store.save(state)

        try:
            info = await ctx.client.get_book(book_id)
        except Exception as exc:
            log.exception("book fetch failed: %s", exc)
            await status(
                "<b>FLIBUSTA / DOWNLOAD</b>\n\n"
                f"Ошибка загрузки книги: {esc(str(exc)[:300])}",
                main_menu_kb(),
            )
            state.screen = "main"
            await ctx.state_store.save(state)
            return

        tmpdir = Path(ctx.settings.temp_path) / str(book_id)
        tmpdir.mkdir(parents=True, exist_ok=True)
        epub_path = None

        async def fetch_epub(mirror):
            http = ctx.client.http_for(mirror)
            dest = tmpdir / f"{book_id}.epub"
            await ctx.download_epub(
                http,
                epub_url(mirror.url, book_id),
                dest,
                timeout=ctx.settings.download_timeout,
            )
            if not await asyncio.to_thread(validate_epub, dest):
                dest.unlink(missing_ok=True)
                raise ValueError("Invalid EPUB")
            return dest

        async def fetch_fb2(mirror):
            http = ctx.client.http_for(mirror)
            return await ctx.download_fb2(
                http,
                fb2_url(mirror.url, book_id),
                tmpdir,
                timeout=ctx.settings.download_timeout,
            )

        try:
            if info.has_epub:
                await status("<b>FLIBUSTA / DOWNLOAD</b>\n\nСкачивание EPUB...")
                try:
                    epub_path, _mirror = await ctx.mirrors.run_with_failover(
                        fetch_epub
                    )
                    if not await asyncio.to_thread(validate_epub, epub_path):
                        epub_path.unlink(missing_ok=True)
                        raise ValueError("Invalid EPUB")
                except Exception as exc:
                    log.warning("EPUB download failed: %s", exc)
                    epub_path = None
                    if not info.has_fb2:
                        raise

            if epub_path is None and info.has_fb2:
                await status("<b>FLIBUSTA / DOWNLOAD</b>\n\nСкачивание FB2...")
                fb2_path, _mirror = await ctx.mirrors.run_with_failover(fetch_fb2)
                await status(
                    "<b>FLIBUSTA / DOWNLOAD</b>\n\n"
                    "Преобразование FB2 → EPUB..."
                )
                epub_path = tmpdir / f"{book_id}.epub"
                await asyncio.to_thread(ctx.convert, fb2_path, epub_path)
                if not await asyncio.to_thread(validate_epub, epub_path):
                    raise ValueError("Invalid EPUB after conversion")

            if epub_path is None:
                await status(
                    "<b>FLIBUSTA / DOWNLOAD</b>\n\nEPUB/FB2 недоступны.",
                    book_kb(book_id, info.has_epub, info.has_fb2),
                )
                state.screen = "book"
                state.book_id = book_id
                await ctx.state_store.save(state)
                return

            await status("<b>FLIBUSTA / DOWNLOAD</b>\n\nОтправка файла...")
            await cb.bot.send_document(
                chat_id=chat_id,
                document=FSInputFile(
                    str(epub_path),
                    filename=filename_for(info.title, book_id),
                ),
            )

            state.screen = "book"
            state.book_id = book_id
            await status(
                "<b>FLIBUSTA / DOWNLOAD</b>\n\n✅ Книга отправлена.\n\n"
                + render_book(info),
                book_kb(book_id, info.has_epub, info.has_fb2),
            )
        except Exception as exc:
            log.exception("download failed: %s", exc)
            state.screen = "book"
            state.book_id = book_id
            await status(
                "<b>FLIBUSTA / DOWNLOAD</b>\n\n"
                f"Ошибка: {esc(str(exc)[:300])}",
                book_kb(book_id, info.has_epub, info.has_fb2),
            )
        finally:
            shutil.rmtree(tmpdir, ignore_errors=True)
            state.screen = "book"
            state.book_id = book_id
            await ctx.state_store.save(state)
