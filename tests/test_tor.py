"""Tor manager basic tests (without a real Tor daemon)."""

from pathlib import Path

from knigojor.network.tor import TorManager


def make_manager(tmp_path: Path) -> TorManager:
    return TorManager(
        executable=str(tmp_path / "missing-tor"),
        socks_host="127.0.0.1",
        socks_port=1,
        control_port=2,
        state_dir=tmp_path / "tor",
    )


def test_proxy_url(tmp_path):
    manager = make_manager(tmp_path)
    assert manager.proxy_url() == "socks5://127.0.0.1:1"


def test_is_installed_false(tmp_path):
    assert make_manager(tmp_path).is_installed() is False


async def test_probe_false_without_daemon(tmp_path):
    manager = make_manager(tmp_path)
    assert await manager.probe() is False
    assert manager.last_probe_ok is False
