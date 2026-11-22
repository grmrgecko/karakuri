# Karakuri Assist for Home Assistant

This custom integration makes a deployed Karakuri flow selectable as the
conversation agent in a Home Assistant Assist pipeline. Home Assistant handles
wake word detection, speech-to-text, and text-to-speech. Karakuri receives the
text and chat transcript, runs the graph, and returns the text Home Assistant
speaks.

## Install

Download `karakuri-assist-<version>.zip` from
[GitHub releases](https://github.com/grmrgecko/karakuri/releases), using the same
release as your Karakuri server. Unzip it into Home Assistant's config directory,
which puts the integration in `custom_components/karakuri_assist`, then restart
Home Assistant:

```sh
cd /config
unzip karakuri-assist-<version>.zip
```

From a checkout, copy `custom_components/karakuri_assist` into
`config/custom_components/` instead. The server release archive also carries it
under `homeassistant/`.

The component uses Home Assistant's current `ConversationEntity` and chat log
interfaces. It targets Home Assistant 2026.8 and later.

## Build the flow

Create a flow with **HA Assist**, **AI Model**, and **AI Basic Chain**, then:

1. On **HA Assist**, select the Home Assistant connection and choose an Agent
   ID. The default is `default`.
2. On **AI Model**, select an AI connection and its model, and wire the model
   into the chain.
3. On **AI Basic Chain**, set **Input** to **Input**, and wire the chain's
   **Response** output to **Response** along the bottom of HA Assist. Wire
   **Out** to a Debug node if you want the answer in run history.
4. Deploy and enable the flow. A draft or disabled flow does not subscribe to
   Assist requests.

The integration waits 120 seconds for a response by default. Lower it once the
model reliably answers sooner.

## Configure Home Assistant

In **Settings > Devices & services**, add **Karakuri Assist** and enter:

- **Agent name**: the label shown in the Assist pipeline picker.
- **Agent ID**: the route name on the HA Assist node, such as `default`.
- **Response timeout**: how long Home Assistant waits for the flow.

The component fires a `karakuri_assist_request` event. Karakuri receives that
event through the Home Assistant connection selected on the node and returns
the result with `karakuri_assist.respond`. Home Assistant does not connect to
the Karakuri HTTP server, and there is no second token to configure.

Select the new conversation entity under **Settings > Voice assistants**, in
the Assist pipeline used by the voice satellite. Turn on **Prefer handling
commands locally** if Home Assistant sentence triggers should run before the
Karakuri agent.

## Request and response

Each turn sent to Karakuri has this shape:

```json
{
  "request_id": "...",
  "agent_id": "default",
  "text": "Is the garage closed?",
  "conversation_id": "01J...",
  "language": "en-US",
  "device_id": "...",
  "satellite_id": "assist_satellite.kitchen",
  "area_id": "kitchen",
  "context": {"id": "...", "user_id": "..."},
  "transcript": [
    {"role": "user", "text": "Is the garage closed?"}
  ]
}
```

The Response input accepts a text answer, a completed response envelope, or an
AI Stream. The envelope that AI Agent and AI Basic Chain put on their Response
output holds the replayable turns, the provider responses, the session ID of a
provider that keeps its own session, the prompt-cache key, and provider replay
blocks. Home Assistant keeps the envelope intact, appends the next user turn, and
sends it back to Karakuri, so a provider with a server-side session, such as
Hermes Agent, resumes it, and stateless providers hit their cache on the unchanged
prompt prefix.

HA Assist speaks the final assistant turn from the envelope. The complete
envelope is exposed under the speech response's `extra_data`; text-to-speech
reads only the answer.

Set **Continue conversation** to **AI decides** when the model should choose
whether Home Assistant listens for another turn. Set the AI Agent or AI Basic
Chain response format to JSON and use this response schema:

```json
{
  "type": "object",
  "properties": {
    "response": { "type": "string" },
    "continue_conversation": { "type": "boolean" }
  },
  "required": ["response", "continue_conversation"]
}
```

Tell the model when another turn is needed in its system prompt. HA Assist
speaks `response` and passes `continue_conversation` to Home Assistant. It
keeps the full response envelope for the next turn. Streaming waits for the
complete JSON response before speaking; tool progress still appears while the
model works.

For streaming, enable **Stream** on AI Agent or AI Basic Chain. Its existing
**Response** output changes to the `AI Stream` type. Keep that output wired to
**Response** on HA Assist. HA Assist forwards each text delta, then closes the
turn with the final answer and stores the complete response envelope for the
next request. Tool progress from a provider that runs its own tools, such as
Hermes Agent, appears as external tool calls in the Home Assistant chat log. Home
Assistant displays the activity but does not run the tools again.
