# Research: openHAB connection

Probed against `openhab/openhab:latest` (openHAB 5.2.3) in Docker on
2026-11-06, with items defined in a file
(`test/openhab/conf/items/karakuri.items`).

## REST API

- `GET /rest/` answers `{version: "8", runtimeInfo: {version: "5.2.3"}}`; the
  connection test reads the runtime version from it. `/rest` without the slash
  is a 302 to it.
- `GET /rest/items?recursive=false&metadata=semantics` lists every item:
  `name`, `label`, `type` (`Switch`, `Dimmer`, `Color`, `Rollershutter`,
  `Number`, `Number:Temperature`, `String`, `Contact`, `DateTime`, `Player`,
  `Location`, `Group`, ...), `state`, `groupNames`, `tags`, `stateDescription`
  (`minimum`, `maximum`, `step`, `pattern`, `readOnly`, `options`), and for
  groups `groupType` and `function`. Unset states are the string `NULL`.
- `POST /rest/items/{name}` with a `text/plain` body sends a command. A
  command of the wrong type is `400`; a Contact takes no commands (`400` for
  `OPEN`). A command to a group goes to its members (`gLights OFF` turned the
  dimmer to 0).
- `GET /rest/items/{name}/state` answers the bare state.

## State formats seen

| Type | State | Commands |
| --- | --- | --- |
| Switch | `ON`, `OFF` | `ON`, `OFF` |
| Contact | `OPEN`, `CLOSED` | none |
| Dimmer | `0` to `100` | percent, `ON`, `OFF`, `INCREASE`, `DECREASE` |
| Color | `h,s,b` (`120,100,50`) | `h,s,b`, percent, `ON`, `OFF` |
| Rollershutter | `0` (open) to `100` (closed) | percent, `UP`, `DOWN`, `STOP` |
| Number | `70`; with a dimension `21.5 °C`, and `unitSymbol` on the item even while NULL | a number, with or without a unit |
| String | text | text |
| DateTime | `2026-11-06T20:00:00.000+0000` | ISO 8601 |
| Player | `PLAY`, `PAUSE` | `PLAY`, `PAUSE`, `NEXT`, `PREVIOUS`, `REWIND`, `FASTFORWARD` |
| Location | `lat,lon,alt` | the same |

openHAB does **not** enforce a state description: `Volume 70` with
`maximum: 50` and `Mode bogus` outside its options were both accepted. The
checks FR-008 asks for are karakuri's alone.

## Events

`GET /rest/events?topics=openhab/items/*/statechanged,openhab/items/*/added,
openhab/items/*/removed,openhab/items/*/updated` is a server-sent event
stream:

```text
event: message
data: {"topic":"openhab/items/KitchenSwitch/statechanged","payload":"{\"type\":\"OnOff\",\"value\":\"OFF\",\"oldType\":\"OnOff\",\"oldValue\":\"ON\",...}","type":"ItemStateChangedEvent"}

event: alive
data: {"type":"ALIVE","interval":10}
```

The payload is a JSON string. `added` and `removed` carry the item (`type`,
`name`, `label`, `tags`, `groupNames`); `updated` carries the new and old
item. An `alive` event every 10 s is the keepalive: no event of any kind for
30 s means the stream is dead and the driver reconnects. A rename of an item
in a file arrives as `removed` then `added`.

Decision: follow `statechanged` (not `statechanged` plus `stateupdated`), since
a device reports changes, and a trigger compares old and new anyway; and
`added`, `removed`, and `updated` to keep the item copy current without
rereading the list.

## Authentication

With implicit user role on (openHAB's default), anonymous clients read and
send commands; a wrong token is `401` rather than falling back to anonymous.
Decision: the API token is optional in the connection, sent as
`Authorization: Bearer <token>` when set, and the connection test says which
it used. A command refused with `401` or `403` reports authorization.

A token is created without the web UI through the Karaf console, fed on
stdin (it ignores commands passed as arguments):

```sh
printf 'openhab:users add admin <password> administrator\nopenhab:users addApiToken admin karakuri items\nlogout\n' |
  docker exec -i <container> /openhab/runtime/bin/client -h 127.0.0.1 -u openhab -p habopen
```

## Semantic locations

`metadata=semantics` gives each tagged item a class and relations: a location
group has `value: Location_Indoor_Room_Kitchen` and `isPartOf` its floor; a
point has `hasLocation` a location, or `isPointOf` an equipment that has one.
Untagged items (Volume, Mode) have none.

Decision: an item's location is, in order, its `hasLocation`; its equipment's
location through `isPointOf`; else the nearest group among its ancestors whose
semantics is a `Location_*`. The floor is the location's `isPartOf` when that
is a `Location_*_Floor_*`. Items with no location sit in no area.

## Kind mapping

| Item type | Kind | Notes |
| --- | --- | --- |
| Switch | boolean | `on` ↔ `ON`/`OFF` |
| Contact | boolean, read only | labels Open and Closed |
| Dimmer | light with brightness | `on` from state > 0; brightness percent; on/off as `ON`/`OFF` |
| Color | light with brightness and colour | hex ↔ HSB; brightness is B |
| Rollershutter | cover with position | position = 100 − state; `open` `UP`, `closed` `DOWN`, `stopped` `STOP`; state `open` when position > 0 |
| Number | number, writable unless read only | min, max, step from the state description; unit from the dimension's state; value parsed from `21.5 °C` |
| String with options | select | options from the state description |
| String | text | |
| DateTime | datetime | RFC 3339 both ways |
| Player | media | state `PLAY` playing, `PAUSE` paused; skip on; no volume, mute, stop, source |
| Location | location | latitude and longitude from `lat,lon,alt` |
| Group with base type | the base type's kind | commands go to the group |
| Group without one, Image, Call | not supported | listed with the reason |

`NULL` and `UNDEF` are not values; a device on such an item is unknown.

## Driver shape

A Go HTTP client with a long-lived SSE reader on its own goroutine, read with
`bufio.Scanner` over `event:` and `data:` lines; no dependency is needed. The
same pieces as the Home Assistant driver: status, reconnect with 1 s doubling
to 30 s, a known-state memory for the catch-up (read all items after the
stream is open, compare with what was known), and a fan-out of item changes.

## Testing

- Unit tests run against an `openhabtest` fake (httptest) serving `/rest/`,
  `/rest/items`, commands, and an SSE stream the test pushes events into, as
  `hatest` does for Home Assistant.
- A tagged suite (`make test TARGET=openhab`) runs against the real container
  from `test/openhab/compose.yaml`, following the time-series suite's pattern
  (`make openhab-up`, `make openhab-down`), with the token written to
  `build/openhab-token`.
