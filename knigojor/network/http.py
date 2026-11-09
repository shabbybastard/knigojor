"""HTTP client wrapper with session reuse, streaming, timeouts."""

from __future__ import annotations

import asyncio
import logging
from pathlib import Path
from typing import Optional

import aiofiles
import aiohttp
from aiohttp_socks import ProxyConnector

from .retry import retry_async

log = logging.getLogger("knigojor.http")

_SOCKS_SCHEMES = ("socks4://", "socks4a://", "socks5://", "socks5h://")


class HttpClient:
    def __init__(
        self,
        timeout: int = 15,
        max_connections: int = 10,
        proxy: Optional[str] = None,
        max_retries: int = 0,
    ):
        self.timeout = aiohttp.ClientTimeout(total=timeout)
        self.max_connections = max_connections
        self.proxy = proxy
        self.max_retries = max(0, int(max_retries))
        self._uses_socks = bool(proxy and proxy.lower().startswith(_SOCKS_SCHEMES))
        self._session: Optional[aiohttp.ClientSession] = None
        self._connector: Optional[aiohttp.BaseConnector] = None

    async def start(self) -> None:
        if self._uses_socks:
            # aiohttp's ``proxy=`` only supports HTTP proxies.  SOCKS5 is
            # handled at connector level by aiohttp-socks.
            self._connector = ProxyConnector.from_url(
                self.proxy,
                limit=self.max_connections,
                ttl_dns_cache=300,
                enable_cleanup_closed=True,
            )
        else:
            self._connector = aiohttp.TCPConnector(
                limit=self.max_connections,
                ttl_dns_cache=300,
                enable_cleanup_closed=True,
            )
        self._session = aiohttp.ClientSession(
            connector=self._connector,
            timeout=self.timeout,
            headers={"User-Agent": "knigojor/3.0"},
        )

    async def close(self) -> None:
        if self._session is not None:
            await self._session.close()
            self._session = None
        if self._connector is not None:
            await self._connector.close()
            self._connector = None

    @property
    def session(self) -> aiohttp.ClientSession:
        if self._session is None:
            raise RuntimeError("HttpClient not started")
        return self._session

    def _request_kwargs(self, kwargs: dict) -> dict:
        result = dict(kwargs)
        if self.proxy and not self._uses_socks:
            result.setdefault("proxy", self.proxy)
        return result

    async def get(self, url: str, **kwargs) -> aiohttp.ClientResponse:
        return await self.session.get(url, **self._request_kwargs(kwargs))

    @staticmethod
    def _is_retryable(exc: BaseException) -> bool:
        if isinstance(exc, aiohttp.ClientResponseError):
            return exc.status == 429 or 500 <= exc.status < 600
        return isinstance(exc, (aiohttp.ClientError, asyncio.TimeoutError))

    async def get_text(self, url: str, **kwargs) -> str:
        async def op() -> str:
            async with await self.get(url, **kwargs) as resp:
                resp.raise_for_status()
                return await resp.text()

        return await retry_async(
            op,
            max_attempts=self.max_retries + 1,
            retry_on=(aiohttp.ClientError, asyncio.TimeoutError),
            retry_if=self._is_retryable,
        )

    async def download(
        self,
        url: str,
        dest,
        chunk_size: int = 65536,
        max_bytes: int | None = None,
        timeout: int | float | None = None,
        **kwargs,
    ) -> int:
        """Stream download to a file. Returns bytes written.

        The size limit is enforced while streaming, so an unexpectedly large
        file cannot fill the disk.  ``timeout`` overrides the session's total
        timeout for downloads (which are usually much slower than page loads).
        """
        dest = Path(dest)
        kwargs = self._request_kwargs(kwargs)
        if timeout is not None:
            kwargs["timeout"] = aiohttp.ClientTimeout(total=timeout)

        async def op() -> int:
            total = 0
            try:
                async with self.session.get(url, **kwargs) as resp:
                    resp.raise_for_status()
                    async with aiofiles.open(dest, "wb") as f:
                        async for chunk in resp.content.iter_chunked(chunk_size):
                            total += len(chunk)
                            if max_bytes is not None and total > max_bytes:
                                raise ValueError("File too large")
                            await f.write(chunk)
            except Exception:
                dest.unlink(missing_ok=True)
                raise
            return total

        return await retry_async(
            op,
            max_attempts=self.max_retries + 1,
            retry_on=(aiohttp.ClientError, asyncio.TimeoutError),
            retry_if=self._is_retryable,
        )
