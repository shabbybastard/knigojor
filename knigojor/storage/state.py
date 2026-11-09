"""UI state persistence."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Optional

from .database import Database


@dataclass
class UIState:
    user_id: int
    chat_id: Optional[int] = None
    message_id: Optional[int] = None
    screen: str = "main"
    query: Optional[str] = None
    page: int = 0
    book_id: Optional[int] = None
    operation: Optional[str] = None
    extra: dict[str, Any] = field(default_factory=dict)

    def to_row(self) -> tuple:
        return (
            self.user_id,
            self.chat_id,
            self.message_id,
            self.screen,
            self.query,
            self.page,
            self.book_id,
            self.operation,
            json.dumps(self.extra, ensure_ascii=False),
        )

    @classmethod
    def from_row(cls, row) -> "UIState":
        extra = {}
        if row[8]:
            try:
                extra = json.loads(row[8])
            except Exception:
                extra = {}
        return cls(
            user_id=row[0],
            chat_id=row[1],
            message_id=row[2],
            screen=row[3] or "main",
            query=row[4],
            page=row[5] or 0,
            book_id=row[6],
            operation=row[7],
            extra=extra,
        )


class StateStore:
    def __init__(self, db: Database):
        self.db = db

    async def get(self, user_id: int) -> UIState:
        row = await self.db.fetchone(
            "SELECT user_id, chat_id, message_id, screen, query, page, book_id, operation, extra "
            "FROM ui_state WHERE user_id=?",
            (user_id,),
        )
        if row is None:
            return UIState(user_id=user_id)
        return UIState.from_row(row)

    async def save(self, state: UIState) -> None:
        await self.db.execute(
            """INSERT INTO ui_state (user_id, chat_id, message_id, screen, query, page, book_id, operation, extra, updated_at)
               VALUES (?,?,?,?,?,?,?,?,?, strftime('%s','now'))
               ON CONFLICT(user_id) DO UPDATE SET
                 chat_id=excluded.chat_id,
                 message_id=excluded.message_id,
                 screen=excluded.screen,
                 query=excluded.query,
                 page=excluded.page,
                 book_id=excluded.book_id,
                 operation=excluded.operation,
                 extra=excluded.extra,
                 updated_at=excluded.updated_at""",
            state.to_row(),
        )

    async def delete(self, user_id: int) -> None:
        await self.db.execute("DELETE FROM ui_state WHERE user_id=?", (user_id,))
