"""Entry point for knigojor."""

from __future__ import annotations

import asyncio
import html
import logging
import shutil
import sys
import time
from dataclasses import dataclass

try:
    import resource
except ImportError:  # pragma: no cover - Linux is the target platform
    resource = None

from aiogram import Bot, Dispatcher, Router
from aiogram.client.default import DefaultBotProperties
from aiogram.enums import ParseMode
from aiogram.types import ErrorEvent

from . import __version__
from .bot.handlers import book as book_h
from .bot.handlers import download as download_h
from .bot.handlers import search as search_h
from .bot.handlers import start as start_h
from .bot.ui.message import MessageManager
from .config.settings import Settings
from .downloader import converter as conv_mod
from .downloader import download as dl_mod
from .flibusta.client import FlibustaClient
from .network.healthcheck import healthcheck_loop
from .network.http import HttpClient
from .network.mirrors import MirrorManager
from .network.tor import TorManager
from .storage.database import Database
from .storage.mirror_stats import MirrorStatsStore
from .storage.state import StateStore

log = logging.getLogger("knigojor")


@dataclass
class AppContext:
    settings: Settings
    db: Database
    state_store: StateStore
    mirrors: MirrorManager
    tor: TorManager
    http_direct: HttpClient
    http_tor: HttpClient | None
    client: FlibustaClient
    message_manager: MessageManager
    bot: Bot
    download_semaphore: asyncio.Semaphore
    start_time: float = 0.0
    errors: int = 0

    def connection_status(self) -> str:
        available = [m for m in self.mirrors.mirrors if m.is_available()]
        if not available:
            return "Offline"
        if any(m.state.value != "healthy" for m in self.mirrors.mirrors):
            return "Degraded"
        return "Online"

    async def status_text(self) -> str:
        uptime = time.time() - self.start_time
        tor_ok = False
        if self.settings.tor_enabled:
            tor_ok = await self.tor.probe()
        lines = [
            "<b>FLIBUSTA / STATUS</b>",
            "",
            f"Время работы: {int(uptime)} с",
            f"Ошибок за запуск: {self.errors}",
            f"Tor: {'вкл' if self.settings.tor_enabled else 'выкл'} "
            f"({'ok' if tor_ok else 'нет'})",
            "",
            "Зеркала:",
        ]
        for mirror in self.mirrors.status():
            url = html.escape(str(mirror["url"]))
            lines.append(
                f"- {url}: {mirror['state']} ({mirror['failures']} err)"
            )
        if resource is not None:
            rss_mb = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
            lines.append("")
            lines.append(f"RAM: {rss_mb:.0f} MB")
        lines.append(f"Версия: {__version__}")
        return "\n".join(lines)

    async def download_epub(self, http, url, dest, timeout=None):
        return await dl_mod.download_epub(
            http,
            url,
            dest,
            self.settings.max_file_size_mb,
            timeout=timeout,
        )

    async def download_fb2(self, http, url, tmpdir, timeout=None):
        return await dl_mod.download_fb2_zip(
            http,
            url,
            tmpdir,
            self.settings.max_file_size_mb,
            timeout=timeout,
        )

    def convert(self, fb2_path, epub_path):
        return conv_mod.convert_fb2_to_epub(fb2_path, epub_path)


async def main() -> None:
    settings = Settings.load()
    logging.basicConfig(
        level=getattr(logging, settings.log_level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
        stream=sys.stdout,
    )
    log.info("Starting knigojor")

    db = Database(str(settings.database_path))
    await db.connect()
    state_store = StateStore(db)

    mirror_stats = MirrorStatsStore(db)
    mirrors = MirrorManager(
        settings.mirrors,
        settings.onion_mirrors,
        settings.tor_enabled,
        stats_store=mirror_stats,
    )
    await mirrors.load_stats()

    http_direct = HttpClient(
        timeout=settings.http_timeout,
        max_connections=settings.max_parallel_requests,
        max_retries=settings.max_retries,
    )
    await http_direct.start()

    http_tor = None
    tor = TorManager(
        settings.tor_executable,
        settings.tor_socks_host,
        settings.tor_socks_port,
        settings.tor_control_port,
        settings.tor_state_dir,
        settings.tor_enabled,
    )
    if settings.tor_enabled:
        tor_ok = await tor.start()
        mirrors.tor_available = tor_ok
        if tor_ok:
            http_tor = HttpClient(
                timeout=settings.http_timeout,
                max_connections=settings.max_parallel_requests,
                proxy=tor.proxy_url(),
                max_retries=settings.max_retries,
            )
            await http_tor.start()
    else:
        mirrors.tor_available = False

    client = FlibustaClient(mirrors, http_direct, http_tor)

    bot = Bot(
        token=settings.bot_token,
        default=DefaultBotProperties(parse_mode=ParseMode.HTML),
    )
    message_manager = MessageManager()

    ctx = AppContext(
        settings=settings,
        db=db,
        state_store=state_store,
        mirrors=mirrors,
        tor=tor,
        http_direct=http_direct,
        http_tor=http_tor,
        client=client,
        message_manager=message_manager,
        bot=bot,
        download_semaphore=asyncio.Semaphore(settings.max_parallel_downloads),
        start_time=time.time(),
    )

    dp = Dispatcher()
    router = Router()
    start_h.register(router, ctx)
    search_h.register(router, ctx)
    book_h.register(router, ctx)
    download_h.register(router, ctx)
    dp.include_router(router)

    @dp.message.outer_middleware()
    async def auth_msg(handler, event, data):
        if event.from_user and event.from_user.id != settings.allowed_user_id:
            return
        return await handler(event, data)

    @dp.callback_query.outer_middleware()
    async def auth_cb(handler, event, data):
        if event.from_user and event.from_user.id != settings.allowed_user_id:
            await event.answer("Доступ запрещён", show_alert=True)
            return
        return await handler(event, data)

    @dp.errors()
    async def on_error(event: ErrorEvent):
        ctx.errors += 1
        log.error(
            "Unhandled error: %s",
            event.exception,
            exc_info=event.exception,
        )
        update = event.update
        try:
            if update.callback_query is not None:
                await update.callback_query.answer(
                    "Произошла ошибка. Попробуйте ещё раз.",
                    show_alert=True,
                )
            elif update.message is not None:
                await update.message.answer(
                    "Произошла ошибка. Попробуйте ещё раз."
                )
        except Exception:
            pass
        return True

    # Remove files left over from a previous run.
    try:
        for path in settings.temp_path.glob("*"):
            if path.is_file():
                path.unlink(missing_ok=True)
            elif path.is_dir():
                shutil.rmtree(path, ignore_errors=True)
    except Exception:
        log.warning("failed to clean temp directory", exc_info=True)

    health_task = asyncio.create_task(
        healthcheck_loop(
            mirrors,
            http_direct,
            http_tor,
            settings.healthcheck_interval,
        )
    )

    try:
        await dp.start_polling(bot)
    finally:
        health_task.cancel()
        try:
            await health_task
        except asyncio.CancelledError:
            pass
        except Exception:
            log.warning("healthcheck task failed", exc_info=True)
        await bot.session.close()
        await http_direct.close()
        if http_tor is not None:
            await http_tor.close()
        await tor.stop()
        await db.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
