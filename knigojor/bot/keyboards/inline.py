"""Inline keyboard builders."""

from __future__ import annotations

from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup


def main_menu_kb() -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="🔍 Новый поиск", callback_data="new_search")],
        [InlineKeyboardButton(text="🔄 Обновить", callback_data="refresh"),
         InlineKeyboardButton(text="📊 Статус", callback_data="status")],
    ])


def search_prompt_kb() -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="❌ Отмена", callback_data="cancel_search")],
    ])


def results_kb(page: int, total_pages: int, book_ids: list[int]) -> InlineKeyboardMarkup:
    rows = []
    # book selection grid (numbers)
    btns = []
    for i, bid in enumerate(book_ids, start=1):
        btns.append(InlineKeyboardButton(text=str(i), callback_data=f"book:{bid}:{i}"))
        if len(btns) >= 5:
            rows.append(btns)
            btns = []
    if btns:
        rows.append(btns)
    nav = []
    if page > 0:
        nav.append(InlineKeyboardButton(text="⬅️ Пред.", callback_data=f"page:prev:{page}"))
    if page < total_pages - 1:
        nav.append(InlineKeyboardButton(text="➡️ След.", callback_data=f"page:next:{page}"))
    if nav:
        rows.append(nav)
    rows.append([
        InlineKeyboardButton(text="🔍 Новый поиск", callback_data="new_search"),
        InlineKeyboardButton(text="🏠 Меню", callback_data="main_menu"),
    ])
    return InlineKeyboardMarkup(inline_keyboard=rows)


def book_kb(book_id: int, has_epub: bool, has_fb2: bool) -> InlineKeyboardMarkup:
    rows = []
    if has_epub or has_fb2:
        rows.append([InlineKeyboardButton(text="📥 Скачать EPUB", callback_data=f"dl:{book_id}")])
    rows.append([
        InlineKeyboardButton(text="⬅️ К результатам", callback_data="back_results"),
        InlineKeyboardButton(text="🏠 Меню", callback_data="main_menu"),
    ])
    return InlineKeyboardMarkup(inline_keyboard=rows)
