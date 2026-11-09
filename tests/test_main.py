"""AppContext/status tests."""

import time
from types import SimpleNamespace

from knigojor.main import AppContext
from knigojor.network.mirrors import Mirror, MirrorState


class FakeTor:
    def __init__(self):
        self.last_probe_ok = False

    async def probe(self):
        self.last_probe_ok = True
        return True

    def is_running(self):
        return False


class FakeMirrors:
    def __init__(self):
        self.mirrors = [Mirror(url="https://mirror", state=MirrorState.HEALTHY)]

    def status(self):
        return [
            {
                "url": "https://mirror",
                "state": "healthy",
                "failures": 0,
                "onion": False,
                "last_error": "",
            }
        ]


def make_context():
    return AppContext(
        settings=SimpleNamespace(tor_enabled=True),
        db=None,
        state_store=None,
        mirrors=FakeMirrors(),
        tor=FakeTor(),
        http_direct=None,
        http_tor=None,
        client=None,
        message_manager=None,
        bot=None,
        download_semaphore=None,
        start_time=time.time(),
    )


async def test_status_text_reports_fresh_tor_probe():
    ctx = make_context()
    text = await ctx.status_text()

    assert "Tor: вкл (ok)" in text
    assert "Ошибок за запуск: 0" in text
    assert "Версия: 3.0.0" in text
    assert "https://mirror" in text


def test_connection_status():
    ctx = make_context()
    assert ctx.connection_status() == "Online"

    ctx.mirrors.mirrors[0].state = MirrorState.UNAVAILABLE
    assert ctx.connection_status() == "Degraded"
