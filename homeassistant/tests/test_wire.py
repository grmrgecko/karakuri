"""Tests for the Karakuri Assist response contract."""

import importlib.util
from pathlib import Path
import sys
import unittest

WIRE_PATH = (
    Path(__file__).parents[1]
    / "custom_components"
    / "karakuri_assist"
    / "wire.py"
)
SPEC = importlib.util.spec_from_file_location("karakuri_assist_wire", WIRE_PATH)
assert SPEC and SPEC.loader
wire = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = wire
SPEC.loader.exec_module(wire)


class AssistWireTest(unittest.TestCase):
    """Pin the service data shared with Karakuri's response node."""

    def test_transcript_response(self) -> None:
        """A service call retains the spoken answer and transcript metadata."""
        reply = wire.decode_reply(
            {
                "response": "Yes.",
                "conversation_id": "ha-1",
                "session_id": "hermes-1",
                "transcript": {
                    "transcript": [
                        {"role": "user", "text": "Is the café open?"},
                        {"role": "assistant", "text": "Yes."},
                    ],
                    "responses": [{"id": "chatcmpl-1", "choices": []}],
                },
                "continue_conversation": False,
            }
        )
        self.assertEqual(reply.response, "Yes.")
        self.assertFalse(reply.stream)
        self.assertEqual(reply.session_id, "hermes-1")
        self.assertEqual(reply.transcript["responses"][0]["id"], "chatcmpl-1")
        self.assertFalse(reply.continue_conversation)

        chunk = wire.decode_reply({"response": "Yes, ", "stream": True})
        self.assertTrue(chunk.stream)
        whitespace = wire.decode_reply({"response": "\n", "stream": True})
        self.assertEqual(whitespace.response, "\n")
        progress = wire.decode_reply(
            {
                "response": "",
                "stream": True,
                "tool_call_id": "call-ha-1",
                "tool_name": "ha_get_state",
                "tool_label": "Read garage door",
                "tool_status": "running",
            }
        )
        self.assertEqual(progress.tool_call_id, "call-ha-1")
        self.assertEqual(progress.tool_status, "running")

        bad_replies = (
            "not an object",
            {"transcript": []},
            {"response": "\n"},
            {"response": "ok", "transcript": {}},
            {"response": "ok", "transcript": {"transcript": {}}},
        )
        for value in bad_replies:
            with self.subTest(value=value):
                with self.assertRaises(wire.AssistProtocolError):
                    wire.decode_reply(value)

    def test_conversation_history_reuses_returned_transcript(self) -> None:
        """The next turn carries the transcript Karakuri returned."""
        history = wire.TranscriptStore()
        first = [{"role": "user", "text": "Remember moonstone."}]
        self.assertEqual(
            history.for_request("ha-1", "Remember moonstone.", first), first
        )

        returned = [
            *first,
            {"role": "assistant", "text": "I will remember moonstone."},
        ]
        history.remember("ha-1", first, "I will remember moonstone.", returned)

        self.assertEqual(
            history.for_request("ha-1", "What was it?", []),
            [*returned, {"role": "user", "text": "What was it?"}],
        )
        self.assertEqual(
            history.for_request(
                "ha-2", "A separate chat.",
                [{"role": "user", "text": "A separate chat."}],
            ),
            [{"role": "user", "text": "A separate chat."}],
        )

        history.remember(
            "ha-3",
            first,
            "I will remember moonstone.",
            {
                "transcript": returned,
                "responses": [{"id": "chatcmpl-1"}],
                "session_id": "hermes-1",
                "cache_key": "ha-3",
            },
        )
        self.assertEqual(
            history.for_request("ha-3", "What was it?", []),
            {
                "transcript": [
                    *returned,
                    {"role": "user", "text": "What was it?"},
                ],
                "responses": [{"id": "chatcmpl-1"}],
                "session_id": "hermes-1",
                "cache_key": "ha-3",
            },
        )

        history.remember(
            "ha-4",
            {"transcript": first, "cache_key": "ha-4"},
            "I will remember moonstone.",
            None,
        )
        self.assertEqual(
            history.for_request("ha-4", "What was it?", []),
            {
                "transcript": [
                    *first,
                    {"role": "assistant", "text": "I will remember moonstone."},
                    {"role": "user", "text": "What was it?"},
                ],
                "cache_key": "ha-4",
            },
        )


if __name__ == "__main__":
    unittest.main()
