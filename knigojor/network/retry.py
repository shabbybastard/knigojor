"""Retry with exponential backoff and jitter."""

from __future__ import annotations

import asyncio
import logging
import random
from typing import Awaitable, Callable, TypeVar

log = logging.getLogger("knigojor.retry")

T = TypeVar("T")


async def retry_async(
    func: Callable[[], Awaitable[T]],
    *,
    max_attempts: int = 3,
    base_delay: float = 1.0,
    max_delay: float = 8.0,
    retry_on: tuple[type[BaseException], ...] = (Exception,),
    retry_if: Callable[[BaseException], bool] | None = None,
) -> T:
    max_attempts = max(1, max_attempts)
    last_exc: BaseException | None = None
    for attempt in range(1, max_attempts + 1):
        try:
            return await func()
        except retry_on as exc:
            if retry_if is not None and not retry_if(exc):
                raise
            last_exc = exc
            if attempt == max_attempts:
                break
            delay = min(base_delay * (2 ** (attempt - 1)), max_delay)
            delay = delay * (0.5 + random.random() * 0.5)
            log.warning(
                "retry attempt %d/%d after %.2fs: %s",
                attempt,
                max_attempts,
                delay,
                exc,
            )
            await asyncio.sleep(delay)
    if last_exc is None:
        raise RuntimeError("retry_async called without attempts")
    raise last_exc
