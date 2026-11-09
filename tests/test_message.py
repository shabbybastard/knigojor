"""Message length helper tests."""

from knigojor.bot.ui.message import truncate_message


def test_short_message_is_unchanged():
    assert truncate_message("hello") == "hello"


def test_long_message_is_truncated():
    text = "\n".join(f"line {i}" for i in range(2000))
    result = truncate_message(text)
    assert len(result) <= 4000
    assert result.endswith("<i>…</i>")
