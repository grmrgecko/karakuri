# Node contracts

All seven nodes live under Network. Input formats are text (default), binary,
base64 and JSON, with catalog-derived string/binary/any types. Actions preserve
the original envelope and have out/error ports; TCP Request replaces payload
with the decoded response. Input triggers emit on out and support test_params.

| Type | Connection | Behavior |
| --- | --- | --- |
| tcp-in | tcp client/listener | Subscribe to configured bounded frames. |
| tcp-out | tcp client/listener | Send to peer, incoming session or explicit broadcast. |
| tcp-request | tcp client/request-only | Isolated exchange with delimiter/fixed/close/idle response. |
| udp-in | udp | Emit one message per datagram, including empty datagrams. |
| udp-out | udp | Send one datagram to configured/template destination or reply peer. |
| websocket-in | websocket | Subscribe to native text/binary messages. |
| websocket-out | websocket | Send a selected frame to peer, reply session or broadcast. |

Action input ports derive string, binary or any from the send format, including
base64 conversion at the REST/MCP test boundary. Outgoing encoding is text,
binary, base64 or JSON. Text requires a string,
binary requires bytes, base64 decodes text, and JSON serializes the payload.
TCP/UDP send bytes; WebSocket binary/base64 use binary frames, text/JSON use text.
TCP output/request may append an escaped delimiter. TCP output may close after
write. Listener broadcast is explicit. An expired or foreign reply never selects
a new session. Node failures suppress automatic retries.

TCP Request config includes response mode, escaped delimiter, fixed length,
include-delimiter, idle gap, overall timeout and response format. Empty response
at orderly close is valid; premature fixed/delimiter EOF is an error. The overall
timeout never becomes a partial success. Idle mode waits for its first byte,
then succeeds only on its shorter idle gap.
