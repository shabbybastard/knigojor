"""Single-message UI manager."""

from __future__ import annotations

import asyncio
import logging
from typing import Optional

from aiogram import Bot
from aiogram.exceptions import TelegramBadRequest, TelegramRetryAfter
from aiogram.types import InlineKeyboardMarkup

log = logging.getLogger("knigojor.ui")

MAX_MESSAGE_LENGTH = 4096
_SAFE_LIMIT = MAX_MESSAGE_LENGTH - 96


def truncate_message(text: str, limit: int = _SAFE_LIMIT) -> str:
    """Trim generated HTML on line boundaries to fit Telegram's limit."""
    if len(text) <= limit:
        return text
    lines = text.splitlines()
    kept: list[str] = []
    size = 0
    for line in lines:
        if size + len(line) + 1 > limit - 20:
            break
        kept.append(line)
        size += len(line) + 1
    if not kept:
        return text[: limit - 20] + "\n\n<i>…</i>"
    return "\n".join(kept) + "\n\n<i>…</i>"


class MessageManager:
    """Manages the single main interface message per user."""

    def __init__(self):
        self._locks: dict[int, asyncio.Lock] = {}

    def lock(self, user_id: int) -> asyncio.Lock:
        if user_id not in self._locks:
            self._locks[user_id] = asyncio.Lock()
        return self._locks[user_id]

    async def send_or_edit(
        self,
        bot: Bot,
        chat_id: int,
        message_id: Optional[int],
        text: str,
        reply_markup: Optional[InlineKeyboardMarkup] = None,
    ) -> int:
        text = truncate_message(text)
        if message_id is not None:
            for attempt in range(2):
                try:
                    await bot.edit_message_text(
                        chat_id=chat_id,
                        message_id=message_id,
                        text=text,
                        reply_markup=reply_markup,
                        parse_mode="HTML",
                    )
                    return message_id
                except TelegramRetryAfter as exc:
                    if attempt == 0:
                        log.warning("flood limit, retrying after %ss", exc.retry_after)
                        await asyncio.sleep(exc.retry_after)
                        continue
                    raise
                except TelegramBadRequest as exc:
                    message = str(exc).lower()
                    if "message is not modified" in message:
                        return message_id
                    if (
                        "message to edit not found" in message
                        or "message can't be edited" in message
                    ):
                        break
                    log.warning("edit failed: %s", exc)
                    break
                except Exception as exc:
                    log.warning("edit error: %s", exc)
                    break

        for attempt in range(2):
            try:
                message = await bot.send_message(
                    chat_id=chat_id,
                    text=text,
                    reply_markup=reply_markup,
                    parse_mode="HTML",
                )
                return message.message_id
            except TelegramRetryAfter as exc:
                if attempt == 0:
                    log.warning("flood limit, retrying after %ss", exc.retry_after)
                    await asyncio.sleep(exc.retry_after)
                    continue
                raise
        raise RuntimeError("unreachable")
