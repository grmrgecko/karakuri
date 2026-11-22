"""Conversation entity for Karakuri Assist."""

import json
import logging
from typing import Any, Literal, override

from homeassistant.components import conversation
from homeassistant.const import MATCH_ALL
from homeassistant.core import HomeAssistant
from homeassistant.helpers import device_registry as dr, entity_registry as er, intent, llm
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from . import KarakuriAssistConfigEntry
from .wire import AssistProtocolError, AssistReply, TranscriptStore

_LOGGER = logging.getLogger(__name__)


async def async_setup_entry(
    hass: HomeAssistant,
    config_entry: KarakuriAssistConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Set up the conversation entity."""
    async_add_entities([KarakuriConversationEntity(config_entry)])


class KarakuriConversationEntity(
    conversation.ConversationEntity, conversation.AbstractConversationAgent
):
    """A Home Assistant conversation agent backed by a Karakuri flow."""

    _attr_supports_streaming = True
    _attr_supported_features = conversation.ConversationEntityFeature.CONTROL

    def __init__(self, entry: KarakuriAssistConfigEntry) -> None:
        """Initialize the agent."""
        self._entry = entry
        self._attr_name = entry.title
        self._attr_unique_id = entry.entry_id
        self._history = TranscriptStore()

    @property
    @override
    def supported_languages(self) -> list[str] | Literal["*"]:
        """Return the languages accepted by the flow."""
        return MATCH_ALL

    @override
    async def async_added_to_hass(self) -> None:
        """Register this entity as a selectable Assist agent."""
        await super().async_added_to_hass()
        conversation.async_set_agent(self.hass, self._entry, self)

    @override
    async def async_will_remove_from_hass(self) -> None:
        """Remove this entity from the Assist agent list."""
        conversation.async_unset_agent(self.hass, self._entry)
        await super().async_will_remove_from_hass()

    @override
    async def _async_handle_message(
        self,
        user_input: conversation.ConversationInput,
        chat_log: conversation.ChatLog,
    ) -> conversation.ConversationResult:
        """Send the current turn and transcript to Karakuri."""
        transcript = self._history.for_request(
            chat_log.conversation_id,
            user_input.text,
            _transcript(chat_log),
        )
        payload = {
            "text": user_input.text,
            "conversation_id": chat_log.conversation_id,
            "language": user_input.language,
            "device_id": user_input.device_id,
            "satellite_id": user_input.satellite_id,
            "area_id": _area_id(self.hass, user_input),
            "context": user_input.context.as_dict(),
            "transcript": transcript,
        }
        reply: AssistReply | None = None
        streamed = ""
        assistant_started = False

        async def reply_stream():
            """Translate Karakuri service calls into Home Assistant deltas."""
            nonlocal assistant_started, reply, streamed

            def content_delta(content: str) -> dict[str, str]:
                nonlocal assistant_started
                delta = {"content": content}
                if not assistant_started:
                    delta["role"] = "assistant"
                    assistant_started = True
                return delta

            async for incoming in self._entry.runtime_data.async_process(payload):
                if incoming.tool_status == "running":
                    yield {
                        "role": "assistant",
                        "tool_calls": [
                            llm.ToolInput(
                                id=incoming.tool_call_id or "",
                                tool_name=incoming.tool_name or "",
                                tool_args={},
                                external=True,
                            )
                        ],
                    }
                    assistant_started = True
                    continue
                if incoming.tool_status == "completed":
                    yield {
                        "role": "tool_result",
                        "tool_call_id": incoming.tool_call_id or "",
                        "tool_name": incoming.tool_name or "",
                        "tool_result": {"status": "completed"},
                    }
                    assistant_started = False
                    continue
                if incoming.stream:
                    streamed += incoming.response
                    yield content_delta(incoming.response)
                    continue
                reply = incoming
                if incoming.response.startswith(streamed):
                    if remainder := incoming.response[len(streamed) :]:
                        yield content_delta(remainder)
                elif not streamed:
                    yield content_delta(incoming.response)
                yield {
                    "native": {
                        "session_id": incoming.session_id,
                        "transcript": incoming.transcript,
                    }
                }

        try:
            async for _ in chat_log.async_add_delta_content_stream(
                self.entity_id, reply_stream()
            ):
                pass
            # A stream that ends without a final reply is a protocol failure
            # like any other, and gets the same spoken error rather than an
            # unhandled exception for the turn.
            if reply is None:
                raise AssistProtocolError(
                    "karakuri stream ended without a final response"
                )
        except AssistProtocolError as err:
            _LOGGER.warning("karakuri assist request failed: %s", err)
            response = intent.IntentResponse(language=user_input.language)
            response.async_set_error(
                intent.IntentResponseErrorCode.UNKNOWN,
                "Karakuri did not answer. Check the Karakuri flow and Home Assistant logs.",
            )
            return conversation.ConversationResult(
                response=response, conversation_id=chat_log.conversation_id
            )

        self._history.remember(
            chat_log.conversation_id,
            transcript,
            reply.response,
            reply.transcript,
        )
        response = intent.IntentResponse(language=user_input.language)
        extra_data = {
            key: value
            for key, value in {
                "conversation_id": reply.conversation_id,
                "session_id": reply.session_id,
                "transcript": reply.transcript,
            }.items()
            if value is not None
        }
        response.async_set_speech(reply.response, extra_data=extra_data or None)
        follow_up = (
            chat_log.continue_conversation
            if reply.continue_conversation is None
            else reply.continue_conversation
        )
        return conversation.ConversationResult(
            response=response,
            conversation_id=chat_log.conversation_id,
            continue_conversation=follow_up,
        )


def _transcript(chat_log: conversation.ChatLog) -> list[dict[str, Any]]:
    """Render the HA chat log in Karakuri's role/text format."""
    transcript: list[dict[str, Any]] = []
    for item in chat_log.content:
        role = item.role
        if role == "tool_result":
            text = json.dumps(item.tool_result, ensure_ascii=False, default=str)
        else:
            text = item.content or ""
        transcript.append({"role": role, "text": text})
    return transcript


def _area_id(
    hass: HomeAssistant, user_input: conversation.ConversationInput
) -> str | None:
    """Resolve the room from the voice device or Assist satellite."""
    device_registry = dr.async_get(hass)
    if user_input.device_id and (
        device := device_registry.async_get(user_input.device_id)
    ):
        if device.area_id:
            return device.area_id

    if not user_input.satellite_id:
        return None
    entity = er.async_get(hass).async_get(user_input.satellite_id)
    if entity and entity.area_id:
        return entity.area_id
    if entity and entity.device_id and (
        device := device_registry.async_get(entity.device_id)
    ):
        return device.area_id
    return None
