"""Centralized configuration loaded from environment / .env file."""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlparse


def _load_dotenv(path: Path) -> None:
    if not path.exists():
        return
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        k = k.strip()
        v = v.strip().strip('"').strip("'")
        if k and k not in os.environ:
            os.environ[k] = v


def _home() -> Path:
    return Path(os.path.expanduser("~"))


def _base_dir() -> Path:
    return _home() / ".knigojor"


@dataclass
class Settings:
    bot_token: str
    allowed_user_id: int

    log_level: str = "INFO"

    base_dir: Path = field(default_factory=_base_dir)
    database_path: Path = field(init=False)
    temp_path: Path = field(init=False)

    tor_enabled: bool = True
    tor_executable: str = "/usr/bin/tor"
    tor_socks_host: str = "127.0.0.1"
    tor_socks_port: int = 9050
    tor_control_port: int = 9051
    tor_state_dir: Path = field(init=False)

    http_timeout: int = 15
    download_timeout: int = 180
    healthcheck_interval: int = 300

    max_retries: int = 3
    max_parallel_requests: int = 5
    max_parallel_downloads: int = 1

    mirrors: list[str] = field(default_factory=list)
    onion_mirrors: list[str] = field(default_factory=list)

    max_file_size_mb: int = 50
    page_size: int = 5

    def __post_init__(self) -> None:
        self.base_dir.mkdir(parents=True, exist_ok=True)
        self.database_path = self.base_dir / "knigojor.db"
        self.temp_path = self.base_dir / "temp"
        self.temp_path.mkdir(parents=True, exist_ok=True)
        self.tor_state_dir = self.base_dir / "tor_state"
        self.tor_state_dir.mkdir(parents=True, exist_ok=True)
        for path in (self.base_dir, self.temp_path, self.tor_state_dir):
            try:
                path.chmod(0o700)
            except OSError:
                pass

    @classmethod
    def load(cls, env_file: Path | None = None) -> "Settings":
        if env_file is None:
            env_file = _base_dir() / ".env"
        if env_file.exists():
            try:
                env_file.chmod(0o600)
            except OSError:
                pass
            _load_dotenv(env_file)

        def _env(name: str, default: str | None = None) -> str | None:
            return os.environ.get(name, default)

        def _int(
            name: str,
            default: int,
            *,
            minimum: int | None = None,
            maximum: int | None = None,
        ) -> int:
            raw = _env(name, str(default))
            if raw is None or raw.strip() == "":
                raw = str(default)
            try:
                value = int(raw)
            except ValueError as exc:
                raise ValueError(f"{name} must be an integer, got {raw!r}") from exc
            if minimum is not None and value < minimum:
                raise ValueError(f"{name} must be >= {minimum}, got {value}")
            if maximum is not None and value > maximum:
                raise ValueError(f"{name} must be <= {maximum}, got {value}")
            return value

        def _url(value: str, name: str) -> str:
            parsed = urlparse(value)
            if parsed.scheme not in ("http", "https") or not parsed.netloc:
                raise ValueError(f"{name} must be an http(s) URL, got {value!r}")
            return value.rstrip("/")

        token = _env("BOT_TOKEN")
        user_id = _env("ALLOWED_USER_ID")
        if not token:
            raise ValueError("BOT_TOKEN is required (set in ~/.knigojor/.env or env)")
        if not user_id:
            raise ValueError("ALLOWED_USER_ID is required (set in ~/.knigojor/.env or env)")

        mirrors: list[str] = []
        onion: list[str] = []
        for i in range(1, 11):
            value = _env(f"MIRROR_{i}")
            if value:
                mirrors.append(_url(value, f"MIRROR_{i}"))
            onion_value = _env(f"ONION_{i}")
            if onion_value:
                onion.append(_url(onion_value, f"ONION_{i}"))

        if not mirrors:
            mirrors = ["https://flibusta.is", "https://flibusta.lib"]

        return cls(
            bot_token=token,
            allowed_user_id=_int("ALLOWED_USER_ID", 0, minimum=1),
            log_level=_env("LOG_LEVEL", "INFO") or "INFO",
            tor_enabled=(_env("TOR_ENABLED", "true") or "true").lower()
            in ("1", "true", "yes", "on"),
            tor_executable=_env("TOR_EXECUTABLE", "/usr/bin/tor") or "/usr/bin/tor",
            tor_socks_host=_env("TOR_SOCKS_HOST", "127.0.0.1") or "127.0.0.1",
            tor_socks_port=_int("TOR_SOCKS_PORT", 9050, minimum=1, maximum=65535),
            tor_control_port=_int("TOR_CONTROL_PORT", 9051, minimum=1, maximum=65535),
            http_timeout=_int("HTTP_TIMEOUT", 15, minimum=1, maximum=600),
            download_timeout=_int("DOWNLOAD_TIMEOUT", 180, minimum=1, maximum=3600),
            healthcheck_interval=_int(
                "HEALTHCHECK_INTERVAL", 300, minimum=30, maximum=3600
            ),
            max_retries=_int("MAX_RETRIES", 3, minimum=0, maximum=10),
            max_parallel_requests=_int(
                "MAX_PARALLEL_REQUESTS", 5, minimum=1, maximum=100
            ),
            max_parallel_downloads=_int(
                "MAX_PARALLEL_DOWNLOADS", 1, minimum=1, maximum=10
            ),
            mirrors=mirrors,
            onion_mirrors=onion,
            max_file_size_mb=_int("MAX_FILE_SIZE_MB", 50, minimum=1, maximum=50),
            page_size=_int("PAGE_SIZE", 5, minimum=1, maximum=50),
        )
