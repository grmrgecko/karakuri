# Research: Network socket nodes

## Lifecycle and settings

Decision: Three core connection kinds own sockets; nodes own subscriptions.
Research inspected connmgr, core registration, MQTT input and settings metadata.
Rationale: action nodes have no deployment lifecycle; existing connection
settings already provide encrypted credentials and shared startup/shutdown.
Alternative: hidden action-node goroutines or flow-owned listener registries.
Rejected because they duplicate lifecycle and make output-only listeners unclear.

Subscriptions must close on driver replacement and inputs must re-resolve. Merely
copying MQTT's initial subscribe loop would retain a retired driver reference.
WebSocket listeners bind dedicated ports because the plugin HTTP proxy does not
support upgrades and the application's event socket has different authorization.

## Protocol behavior

Decision: Use explicit broadcast and isolated
TCP requests. Framing is owned by the persistent TCP connection, not individual
subscribers. Shared readers cannot apply conflicting framing to the same stream.
A fresh request socket avoids competing consumers and response cross-talk.

Decision: Use existing coder/websocket and x/net packet controls. No dependency
upgrade is needed. Read/write contexts and limits come from the WebSocket API;
IPv4/IPv6 packet options configure multicast interface, hops and membership.
References: [WebSocket API](https://pkg.go.dev/github.com/coder/websocket),
[IPv4](https://pkg.go.dev/golang.org/x/net/ipv4),
[IPv6](https://pkg.go.dev/golang.org/x/net/ipv6).

## Reliability and credentials

Decision: No automatic replay after socket sends, including authored retries.
Opaque session IDs name only currently live sockets in the selected driver.
Rationale: partial delivery is uncertain; retrying or falling back to broadcast
can duplicate commands or target unrelated peers. Queues and sockets are bounded.
TLS and headers use inline encrypted settings; metadata never includes secrets.
Alternative: filesystem key paths or ambient certificates. Rejected because
settings should carry the connection's credentials and preserve existing trust.
