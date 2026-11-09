"""Persistence for mirror health statistics."""

from __future__ import annotations

import logging
from typing import Any

from .database import Database

log = logging.getLogger("knigojor.storage.mirror_stats")


class MirrorStatsStore:
    def __init__(self, db: Database):
        self.db = db

    async def save(self, mirror: Any) -> None:
        await self.db.execute(
            """INSERT INTO mirror_stats (url, status, failures, last_check, last_error)
               VALUES (?,?,?,?,?)
               ON CONFLICT(url) DO UPDATE SET
                 status=excluded.status,
                 failures=excluded.failures,
                 last_check=excluded.last_check,
                 last_error=excluded.last_error""",
            (
                mirror.url,
                mirror.state.value,
                mirror.failures,
                mirror.last_check,
                mirror.last_error,
            ),
        )

    async def load(self) -> list[dict[str, Any]]:
        rows = await self.db.fetchall(
            "SELECT url, status, failures, last_check, last_error FROM mirror_stats"
        )
        return [
            {
                "url": row[0],
                "status": row[1],
                "failures": row[2],
                "last_check": row[3],
                "last_error": row[4],
            }
            for row in rows
        ]
