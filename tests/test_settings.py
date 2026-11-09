"""Settings tests."""

import pytest

from knigojor.config.settings import Settings


def test_settings_requires_token(monkeypatch, tmp_path):
    monkeypatch.delenv("BOT_TOKEN", raising=False)
    monkeypatch.delenv("ALLOWED_USER_ID", raising=False)

    with pytest.raises(ValueError, match="BOT_TOKEN"):
        Settings.load(env_file=tmp_path / "missing.env")


def test_settings_requires_user_id(monkeypatch, tmp_path):
    monkeypatch.setenv("BOT_TOKEN", "token")
    monkeypatch.delenv("ALLOWED_USER_ID", raising=False)

    with pytest.raises(ValueError, match="ALLOWED_USER_ID"):
        Settings.load(env_file=tmp_path / "missing.env")


def test_settings_rejects_invalid_page_size(monkeypatch, tmp_path):
    monkeypatch.setenv("BOT_TOKEN", "token")
    monkeypatch.setenv("ALLOWED_USER_ID", "123")
    monkeypatch.setenv("PAGE_SIZE", "0")

    with pytest.raises(ValueError, match="PAGE_SIZE"):
        Settings.load(env_file=tmp_path / "missing.env")


def test_settings_defaults(monkeypatch, tmp_path):
    monkeypatch.setenv("BOT_TOKEN", "token")
    monkeypatch.setenv("ALLOWED_USER_ID", "123")
    names = [
        "PAGE_SIZE",
        "HTTP_TIMEOUT",
        "DOWNLOAD_TIMEOUT",
        "HEALTHCHECK_INTERVAL",
        "MAX_RETRIES",
        "MAX_FILE_SIZE_MB",
        "MAX_PARALLEL_REQUESTS",
        "MAX_PARALLEL_DOWNLOADS",
        "TOR_ENABLED",
        "TOR_SOCKS_PORT",
        "TOR_CONTROL_PORT",
        "LOG_LEVEL",
    ]
    names += [f"MIRROR_{i}" for i in range(1, 11)]
    names += [f"ONION_{i}" for i in range(1, 11)]
    for name in names:
        monkeypatch.delenv(name, raising=False)

    settings = Settings.load(env_file=tmp_path / "missing.env")
    assert settings.allowed_user_id == 123
    assert settings.page_size == 5
    assert settings.download_timeout == 180
    assert settings.healthcheck_interval == 300
    assert settings.max_retries == 3
    assert settings.mirrors == ["https://flibusta.is", "https://flibusta.lib"]


def test_settings_creates_private_directories(tmp_path):
    settings = Settings(
        bot_token="token",
        allowed_user_id=123,
        base_dir=tmp_path / "config",
    )
    assert settings.database_path == tmp_path / "config" / "knigojor.db"
    assert settings.temp_path.exists()
    assert settings.tor_state_dir.exists()
