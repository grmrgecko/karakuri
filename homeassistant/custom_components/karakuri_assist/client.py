"""Home Assistant event bridge for Karakuri Assist."""

import asyncio
from collections.abc import AsyncIterator
from typing import Any
from uuid import uuid4

from homeassistant.core import HomeAssistant, ServiceCall
from homeassistant.exceptions import ServiceValidationError

from .const import EVENT_ASSIST_REQUEST
from .wire import AssistProtocolError, AssistReply, decode_reply


class KarakuriAssistBroker:
    """Match Home Assistant events with replies from Karakuri."""

    def __init__(self, hass: HomeAssistant) -> None:
        """Initialize the broker."""
        self._hass = hass
        self._pending: dict[str, tuple[str, asyncio.Queue[AssistReply]]] = {}

    async def async_process(
        self, agent_id: str, payload: dict[str, Any], timeout: int
    ) -> AsyncIterator[AssistReply]:
        """Publish one Assist turn and yield stream chunks through the final reply."""
        request_id = uuid4().hex
        queue: asyncio.Queue[AssistReply] = asyncio.Queue()
        self._pending[request_id] = (agent_id, queue)
        event_data = {**payload, "request_id": request_id, "agent_id": agent_id}
        self._hass.bus.async_fire(EVENT_ASSIST_REQUEST, event_data)
        try:
            async with asyncio.timeout(timeout):
                while True:
                    reply = await queue.get()
                    yield reply
                    if not reply.stream:
                        return
        except TimeoutError as err:
            raise AssistProtocolError(
                f"karakuri did not answer within {timeout} seconds"
            ) from err
        finally:
            self._pending.pop(request_id, None)

    async def async_respond(self, call: ServiceCall) -> None:
        """Resolve the Assist turn named by a Karakuri response service call."""
        request_id = call.data["request_id"]
        pending = self._pending.get(request_id)
        if pending is None:
            raise ServiceValidationError("no pending karakuri assist request")
        agent_id, queue = pending
        if call.data["agent_id"] != agent_id:
            raise ServiceValidationError("karakuri assist agent ID does not match")
        try:
            queue.put_nowait(decode_reply(call.data))
        except AssistProtocolError as err:
            raise ServiceValidationError(str(err)) from err


class KarakuriAssistClient:
    """Send conversation turns to one deployed HA Assist node."""

    def __init__(
        self,
        broker: KarakuriAssistBroker,
        agent_id: str,
        timeout: int,
    ) -> None:
        """Initialize the client."""
        self._broker = broker
        self._agent_id = agent_id
        self._timeout = timeout

    async def async_process(self, payload: dict[str, Any]) -> AsyncIterator[AssistReply]:
        """Send one conversation turn and yield its streamed and final replies."""
        async for reply in self._broker.async_process(
            self._agent_id, payload, self._timeout
        ):
            yield reply
