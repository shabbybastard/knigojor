"""UI state persistence tests."""

from knigojor.storage.database import Database
from knigojor.storage.state import StateStore, UIState


async def test_state_round_trip(tmp_path):
    db = Database(str(tmp_path / "state.db"))
    await db.connect()
    try:
        store = StateStore(db)
        state = UIState(
            user_id=1,
            chat_id=2,
            message_id=3,
            screen="results",
            query="Пушкин",
            page=4,
            book_id=5,
            operation="download",
            extra={"total": 100},
        )
        await store.save(state)

        loaded = await store.get(1)
        assert loaded == state

        await store.delete(1)
        assert await store.get(1) == UIState(user_id=1)
    finally:
        await db.close()
