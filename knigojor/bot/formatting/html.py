"""HTML helpers for Telegram."""

from __future__ import annotations

import html


def esc(s, *, quote: bool = False) -> str:
    if s is None:
        return ""
    return html.escape(str(s), quote=quote)
