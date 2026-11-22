# Research: ESPHome native API

## Entities as devices, not a new source

- **Decision**: an added entity is a device in the existing registry, with
  transport `esphome`.
- **Rationale**: the spec first described entities as a dashboard source
  beside Home Assistant entities. Planning counted what that needs: a source
  in `entitySource.ts` and in both entity widgets' bind code, pickers in both
  config panels, a dashboard scope capability, a condition source, a
  reference kind, a history source, and a parallel set of flow nodes. The
  device registry already has every one of these, plus availability and stale
  restore from features 029 and 032. Zigbee2MQTT devices set the precedent:
  discovery creates devices.
- **Alternatives**: mirroring entities into controls, which cannot express
  read-only readings and would let a flow write a value the node never
  reported.

## Protocol library

- **Decision**: `github.com/mycontroller-org/esphome_api` v1.4.0.
- **Rationale**: its `api` package is generated from ESPHome's current
  `api.proto` (it lists water heater, infrared, and radio frequency entities,
  added in 2026); `connection` implements the plaintext framing and the Noise
  `NNpsk0_25519_ChaChaPoly_SHA256` handshake; `client` answers ping, time,
  and disconnect requests. Those packages import only `flynn/noise` and
  protobuf, so the server stays pure Go. Apache-2.0.
- **Alternatives**: vendoring `api.proto` and writing the framing and
  handshake ourselves, about 400 lines plus tracking ESPHome's proto; worth it
  only if the library stalls.

## Discovery

- **Decision**: a small mDNS browser over `x/net/dns/dnsmessage` and
  `x/net/ipv4`: one PTR query for `_esphomelib._tcp.local`, answers collected
  for 3 seconds, SRV, A, and TXT read for host, port, name, MAC, version, and
  board.
- **Rationale**: both packages are already dependencies; a browse is one query
  and a parse. Following a node to a new address is a query for its instance
  name.
- **Alternatives**: `hashicorp/mdns` (adds `miekg/dns`), `grandcat/zeroconf`
  (unmaintained since 2020).

## Keepalive and reconnect

- **Decision**: ping every 20 seconds; drop after 60 seconds without any
  message; backoff 1 second doubling to 60.
- **Rationale**: ESPHome's own client defaults are in the same range, and the
  node itself drops a client it has not heard from in about 150 seconds. The
  spec's "within the keepalive period" is 60 seconds.

## Entity mapping

| ESPHome type | Kind | Writable | Notes |
| --- | --- | --- | --- |
| switch | boolean | yes | `on` |
| binary_sensor | boolean | no | labels from device class (door: Open/Closed) |
| light | light | yes | brightness, colour (rgb), colour temperature from `supported_color_modes`; effects from `effects`; kelvin from mireds |
| cover | cover | yes | state from `current_operation` and `position`; position and tilt when supported |
| fan | fan | yes | speed as a percentage of `supported_speed_count`; oscillation, direction, presets |
| lock | lock | yes | states locked, unlocked, jammed, locking, unlocking |
| climate | climate | yes | modes, target or range, current, action, fan modes |
| number | number | yes | min, max, step, unit |
| select | select | yes | options |
| text | text | yes | max length |
| button | buttons | yes | one button, the entity's name |
| sensor | number | no | unit, accuracy as precision |
| text_sensor | text | no | |
| event | text | no | last event type |

Valve, media player, alarm control panel, siren, update, water heater,
infrared, radio frequency, date, time, datetime, and camera are listed as not
addable; date and time entities were dropped from this feature during
implementation.

## Commands

- **Decision**: `mqttrelay.Command` is the one command path: kind check, then
  transport. The MQTT branch is today's payload script and publish.
- **Rationale**: three callers (REST, MCP, the device node) each repeated
  resolve, script, and publish; a transport branch in three places would
  drift.

## Testing without a node

- **Decision**: a simulated node in `pkg/integrations/esphome/esphometest`
  that accepts a connection, speaks plaintext or Noise (responder side with
  `flynn/noise`), answers hello, device info, entity listing, and commands,
  and pushes states.
- **Rationale**: no node announces itself on the development subnet. Tests
  then cover the session, mapping, commands, and reconnect end to end; a real
  node, when provided, covers interoperability.
