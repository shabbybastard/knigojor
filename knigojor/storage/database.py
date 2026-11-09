"""SQLite database helper with WAL mode."""

from __future__ import annotations

import os

import aiosqlite

SCHEMA = """
CREATE TABLE IF NOT EXISTS ui_state (
    user_id INTEGER PRIMARY KEY,
    chat_id INTEGER,
    message_id INTEGER,
    screen TEXT,
    query TEXT,
    page INTEGER DEFAULT 0,
    book_id INTEGER,
    operation TEXT,
    extra TEXT,
    updated_at REAL DEFAULT (strftime('%s','now'))
);

CREATE TABLE IF NOT EXISTS mirror_stats (
    url TEXT PRIMARY KEY,
    status TEXT,
    failures INTEGER DEFAULT 0,
    last_check REAL,
    last_error TEXT
);
"""


class Database:
    def __init__(self, path: str):
        self.path = path
        self._conn: aiosqlite.Connection | None = None

    async def connect(self) -> None:
        self._conn = await aiosqlite.connect(self.path)
        try:
            os.chmod(self.path, 0o600)
        except OSError:
            pass
        await self._conn.execute("PRAGMA journal_mode=WAL;")
        await self._conn.execute("PRAGMA synchronous=NORMAL;")
        await self._conn.executescript(SCHEMA)
        await self._conn.commit()

    async def close(self) -> None:
        if self._conn is not None:
            await self._conn.close()
            self._conn = None

    @property
    def conn(self) -> aiosqlite.Connection:
        if self._conn is None:
            raise RuntimeError("Database not connected")
        return self._conn

    async def execute(self, sql: str, params: tuple = ()) -> None:
        await self.conn.execute(sql, params)
        await self.conn.commit()

    async def fetchone(self, sql: str, params: tuple = ()):
        async with self.conn.execute(sql, params) as cur:
            return await cur.fetchone()

    async def fetchall(self, sql: str, params: tuple = ()):
        async with self.conn.execute(sql, params) as cur:
            return await cur.fetchall()
