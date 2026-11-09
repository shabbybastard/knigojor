"""Periodic mirror health checks."""

from __future__ import annotations

import asyncio
import logging

log = logging.getLogger("knigojor.healthcheck")


async def check_mirror(http, url: str) -> bool:
    try:
        async with await http.get(url, timeout=10) as resp:
            return resp.status < 500
    except Exception as exc:
        log.debug("healthcheck failed for %s: %s", url, exc)
        return False


async def healthcheck_loop(mirrors, http_direct, http_tor, interval: int = 300) -> None:
    """Probe every configured mirror and persist its state."""
    while True:
        try:
            for mirror in mirrors.all_mirrors():
                http = http_tor if mirror.is_onion else http_direct
                if http is None:
                    continue
                if await check_mirror(http, mirror.url):
                    mirror.mark_success()
                else:
                    mirror.mark_failure("healthcheck failed")
                await mirrors.persist(mirror)
        except asyncio.CancelledError:
            raise
        except Exception:
            log.exception("mirror healthcheck iteration failed")
        await asyncio.sleep(interval)
