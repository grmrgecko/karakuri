"""Response format shared with Karakuri's HA Assist nodes."""

from dataclasses import dataclass
from typing import Any


class AssistProtocolError(ValueError):
    """The Karakuri response does not match the Assist contract."""


@dataclass(frozen=True, slots=True)
class AssistReply:
    """A decoded response returned to the HA Assist node."""

    response: str
    stream: bool
    tool_call_id: str | None
    tool_name: str | None
    tool_label: str | None
    tool_status: str | None
    conversation_id: str | None
    session_id: str | None
    transcript: list[dict[str, Any]] | dict[str, Any] | None
    continue_conversation: bool | None


# Conversations kept per store. Home Assistant expires a conversation after a
# few minutes, but this store only learns of new ones, so the oldest is dropped
# when the cap is reached rather than holding every transcript since startup.
MAX_CONVERSATIONS = 100


class TranscriptStore:
    """Keep the transcript Karakuri returned for each active conversation."""

    def __init__(self) -> None:
        """Initialize an empty conversation store."""
        self._transcripts: dict[
            str, list[dict[str, Any]] | dict[str, Any]
        ] = {}

    def _store(
        self,
        conversation_id: str,
        transcript: list[dict[str, Any]] | dict[str, Any],
    ) -> None:
        """Save a transcript as the most recent, evicting the oldest past the cap."""
        self._transcripts.pop(conversation_id, None)
        self._transcripts[conversation_id] = transcript
        while len(self._transcripts) > MAX_CONVERSATIONS:
            oldest = next(iter(self._transcripts))
            del self._transcripts[oldest]

    def for_request(
        self,
        conversation_id: str,
        text: str,
        chat_log: list[dict[str, Any]],
    ) -> list[dict[str, Any]] | dict[str, Any]:
        """Build a request from saved history or Home Assistant's chat log."""
        previous = self._transcripts.get(conversation_id)
        if previous is None:
            return list(chat_log)
        if isinstance(previous, dict):
            entries = previous.get("transcript", [])
            return {
                **previous,
                "transcript": [*entries, {"role": "user", "text": text}],
            }
        return [*previous, {"role": "user", "text": text}]

    def remember(
        self,
        conversation_id: str,
        sent: list[dict[str, Any]] | dict[str, Any],
        response: str,
        returned: list[dict[str, Any]] | dict[str, Any] | None,
    ) -> None:
        """Save Karakuri's transcript, or reconstruct it when none was returned."""
        if returned is not None:
            self._store(
                conversation_id,
                dict(returned) if isinstance(returned, dict) else list(returned),
            )
            return
        if isinstance(sent, dict):
            entries = sent.get("transcript", [])
            self._store(
                conversation_id,
                {
                    **sent,
                    "transcript": [
                        *entries,
                        {"role": "assistant", "text": response},
                    ],
                },
            )
            return
        self._store(
            conversation_id,
            [*sent, {"role": "assistant", "text": response}],
        )


def decode_reply(value: Any) -> AssistReply:
    """Decode the data from Karakuri's response service call."""
    if not isinstance(value, dict):
        raise AssistProtocolError("karakuri response must be an object")
    stream = value.get("stream", False)
    if not isinstance(stream, bool):
        raise AssistProtocolError("stream must be true or false")
    response = value.get("response")
    if not isinstance(response, str) or (not stream and not response.strip()):
        raise AssistProtocolError("karakuri response has no spoken text")

    tool_status = _optional_string(value.get("tool_status"))
    if tool_status not in (None, "running", "completed"):
        raise AssistProtocolError("karakuri tool status is invalid")

    transcript = value.get("transcript")
    if transcript is not None:
        entries = (
            transcript.get("transcript")
            if isinstance(transcript, dict)
            else transcript
        )
        if not (
            isinstance(entries, list)
            and all(isinstance(entry, dict) for entry in entries)
        ):
            raise AssistProtocolError(
                "karakuri transcript must be an entry list or an envelope containing one"
            )
    follow_up = value.get("continue_conversation")
    if follow_up is not None and not isinstance(follow_up, bool):
        raise AssistProtocolError("continue_conversation must be true or false")
    return AssistReply(
        response=response,
        stream=stream,
        tool_call_id=_optional_string(value.get("tool_call_id")),
        tool_name=_optional_string(value.get("tool_name")),
        tool_label=_optional_string(value.get("tool_label")),
        tool_status=tool_status,
        conversation_id=_optional_string(value.get("conversation_id")),
        session_id=_optional_string(value.get("session_id")),
        transcript=transcript,
        continue_conversation=follow_up,
    )


def _optional_string(value: Any) -> str | None:
    """Return a non-empty string or no value."""
    return value if isinstance(value, str) and value else None
