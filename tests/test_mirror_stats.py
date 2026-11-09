"""Mirror statistics persistence tests."""

from knigojor.network.mirrors import Mirror, MirrorManager, MirrorState
from knigojor.storage.database import Database
from knigojor.storage.mirror_stats import MirrorStatsStore


async def test_mirror_stats_round_trip(tmp_path):
    db = Database(str(tmp_path / "test.db"))
    await db.connect()
    try:
        store = MirrorStatsStore(db)
        mirror = Mirror(url="https://example.test")
        mirror.failures = 2
        mirror.last_error = "timeout"
        mirror.state = MirrorState.DEGRADED
        await store.save(mirror)

        rows = await store.load()
        assert rows == [
            {
                "url": "https://example.test",
                "status": "degraded",
                "failures": 2,
                "last_check": mirror.last_check,
                "last_error": "timeout",
            }
        ]

        manager = MirrorManager(
            ["https://example.test"],
            tor_enabled=False,
            stats_store=store,
        )
        await manager.load_stats()
        assert manager.mirrors[0].failures == 2
        assert manager.mirrors[0].state == MirrorState.DEGRADED
        assert manager.mirrors[0].last_error == "timeout"
    finally:
        await db.close()
