"""Healthcheck task tests."""

import asyncio

import pytest

from knigojor.network.healthcheck import healthcheck_loop


class EmptyMirrors:
    def all_mirrors(self):
        return []

    async def persist(self, mirror):
        return None


async def test_healthcheck_task_can_be_cancelled():
    task = asyncio.create_task(
        healthcheck_loop(EmptyMirrors(), None, None, interval=3600)
    )
    await asyncio.sleep(0)
    task.cancel()

    with pytest.raises(asyncio.CancelledError):
        await task
