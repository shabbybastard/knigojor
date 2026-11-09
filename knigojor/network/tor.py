"""Tor daemon manager."""

from __future__ import annotations

import asyncio
import logging
import os
import shutil
import subprocess
from pathlib import Path

log = logging.getLogger("knigojor.tor")


class TorManager:
    def __init__(
        self,
        executable: str,
        socks_host: str,
        socks_port: int,
        control_port: int,
        state_dir: Path,
        enabled: bool = True,
    ):
        self.executable = executable
        self.socks_host = socks_host
        self.socks_port = socks_port
        self.control_port = control_port
        self.state_dir = state_dir
        self.enabled = enabled
        self.process: subprocess.Popen | None = None
        self.last_probe_ok = False
        self._log_file = None

    def is_installed(self) -> bool:
        return shutil.which(self.executable) is not None or os.path.exists(self.executable)

    def is_running(self) -> bool:
        return self.process is not None and self.process.poll() is None

    async def start(self) -> bool:
        if not self.enabled:
            return False
        if not self.is_installed():
            log.warning("Tor executable not found: %s", self.executable)
            return False
        # If Tor is already running externally, use it.
        if await self.probe():
            log.info("Tor already running on %s:%d", self.socks_host, self.socks_port)
            return True
        try:
            self.state_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
            try:
                self.state_dir.chmod(0o700)
            except OSError:
                pass
            data_dir = self.state_dir / "data"
            data_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
            try:
                data_dir.chmod(0o700)
            except OSError:
                pass

            torrc = self.state_dir / "torrc"
            torrc.write_text(
                f"SocksPort {self.socks_host}:{self.socks_port}\n"
                f"ControlPort {self.control_port}\n"
                f"DataDirectory {data_dir}\n"
                "CookieAuthentication 1\n"
                "Log notice stdout\n"
            )
            self._log_file = open(self.state_dir / "tor.log", "ab", buffering=0)
            self.process = subprocess.Popen(
                [self.executable, "-f", str(torrc)],
                stdout=self._log_file,
                stderr=subprocess.STDOUT,
            )
            # Wait for readiness.
            for _ in range(30):
                await asyncio.sleep(1)
                if await self.probe():
                    log.info("Tor started")
                    return True
            log.error("Tor did not become ready")
            await self.stop()
            return False
        except Exception as exc:
            log.exception("Failed to start Tor: %s", exc)
            await self.stop()
            return False

    async def probe(self) -> bool:
        try:
            reader, writer = await asyncio.wait_for(
                asyncio.open_connection(self.socks_host, self.socks_port),
                timeout=3,
            )
            writer.close()
            try:
                await writer.wait_closed()
            except Exception:
                pass
            self.last_probe_ok = True
            return True
        except Exception:
            self.last_probe_ok = False
            return False

    async def stop(self) -> None:
        if self.process is not None:
            try:
                self.process.terminate()
                await asyncio.to_thread(self.process.wait, 10)
            except Exception:
                try:
                    self.process.kill()
                    await asyncio.to_thread(self.process.wait, 5)
                except Exception:
                    pass
            self.process = None
        self.last_probe_ok = False
        if self._log_file is not None:
            try:
                self._log_file.close()
            except Exception:
                pass
            self._log_file = None

    def proxy_url(self) -> str:
        return f"socks5://{self.socks_host}:{self.socks_port}"
