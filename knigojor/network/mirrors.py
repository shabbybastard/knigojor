"""Mirror manager with circuit breaker."""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass
from enum import Enum

log = logging.getLogger("knigojor.mirrors")


class MirrorState(Enum):
    HEALTHY = "healthy"
    DEGRADED = "degraded"
    UNAVAILABLE = "unavailable"
    HALF_OPEN = "half_open"


@dataclass
class Mirror:
    url: str
    state: MirrorState = MirrorState.HEALTHY
    failures: int = 0
    last_check: float = 0.0
    last_error: str = ""
    is_onion: bool = False

    def mark_success(self) -> None:
        self.state = MirrorState.HEALTHY
        self.failures = 0
        self.last_check = time.time()

    def mark_failure(self, error: str, threshold: int = 3) -> None:
        self.failures += 1
        self.last_error = error
        self.last_check = time.time()
        if self.failures >= threshold:
            self.state = MirrorState.UNAVAILABLE
            log.warning("mirror %s marked unavailable after %d failures", self.url, self.failures)

    def is_available(self) -> bool:
        if self.state in (MirrorState.HEALTHY, MirrorState.DEGRADED, MirrorState.HALF_OPEN):
            return True
        if self.state == MirrorState.UNAVAILABLE:
            # allow half-open after cooldown
            if time.time() - self.last_check > 60.0:
                self.state = MirrorState.HALF_OPEN
                return True
            return False
        return False


class MirrorManager:
    def __init__(
        self,
        mirrors: list[str],
        onion_mirrors: list[str] | None = None,
        tor_enabled: bool = True,
        stats_store=None,
    ):
        self.mirrors: list[Mirror] = [Mirror(url=m, is_onion=False) for m in mirrors]
        self.onion: list[Mirror] = [Mirror(url=m, is_onion=True) for m in (onion_mirrors or [])]
        self.tor_enabled = tor_enabled
        # Set to True by the application only after Tor has actually started.
        self.tor_available = tor_enabled
        self.stats_store = stats_store

    def all_mirrors(self) -> list[Mirror]:
        mirrors = list(self.mirrors)
        if self.tor_enabled and self.tor_available:
            mirrors += self.onion
        return mirrors

    async def load_stats(self) -> None:
        if self.stats_store is None:
            return
        try:
            rows = await self.stats_store.load()
        except Exception:
            log.warning("failed to load mirror stats", exc_info=True)
            return
        by_url = {row["url"]: row for row in rows}
        for mirror in self.mirrors + self.onion:
            row = by_url.get(mirror.url)
            if row is None:
                continue
            try:
                mirror.state = MirrorState(row["status"])
            except ValueError:
                pass
            mirror.failures = int(row["failures"] or 0)
            mirror.last_check = float(row["last_check"] or 0.0)
            mirror.last_error = row["last_error"] or ""

    async def persist(self, mirror: Mirror) -> None:
        if self.stats_store is None:
            return
        try:
            await self.stats_store.save(mirror)
        except Exception:
            log.warning("failed to persist mirror stats", exc_info=True)

    def all_sources(self, include_onion: bool = True) -> list[Mirror]:
        srcs = list(self.mirrors)
        if include_onion and self.tor_enabled and self.tor_available:
            srcs += self.onion
        # healthy first
        return sorted(srcs, key=lambda m: (0 if m.is_available() else 1, m.failures))

    async def run_with_failover(self, operation, *, include_onion: bool = True):
        """operation(mirror) -> awaitable result. Tries mirrors in order."""
        last_exc: Exception | None = None
        for mirror in self.all_sources(include_onion=include_onion):
            if not mirror.is_available():
                continue
            try:
                result = await operation(mirror)
                mirror.mark_success()
                await self.persist(mirror)
                return result, mirror
            except Exception as exc:
                mirror.mark_failure(str(exc))
                await self.persist(mirror)
                last_exc = exc
                log.info("mirror %s failed: %s", mirror.url, exc)
                continue
        if last_exc is not None:
            raise last_exc
        raise RuntimeError("No mirrors available")

    def status(self) -> list[dict]:
        out = []
        for m in self.mirrors + (
            self.onion if self.tor_enabled and self.tor_available else []
        ):
            out.append({
                "url": m.url,
                "state": m.state.value,
                "failures": m.failures,
                "onion": m.is_onion,
                "last_error": m.last_error,
            })
        return out
