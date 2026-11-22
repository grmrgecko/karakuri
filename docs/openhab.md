# openHAB

An openHAB connection follows an openHAB server's items over its REST API and
event stream. Its items can be added as devices, which cards draw and flows
command like any device. Flows can also trigger on item changes, send
commands, and read items. openHAB 4 and later are supported.

## Connecting

Add a connection of kind **openHAB** under **Connections**:

| Field | Meaning |
| --- | --- |
| Base URL | The server, such as `http://openhab.local:8080`, without `/rest`. |
| API token | Optional. openHAB lets anonymous clients read and send commands unless that is turned off. When it is, create a token on the openHAB profile page under API tokens. |

**Test** reports the version, the item count, and whether it authenticated,
such as `openHAB 5.2.3, 18 items, authenticated`, or why it failed:
`unauthorized: check the API token`, `not found: is this an openHAB server?`,
or the network error.

The connection reads every item when it connects, then follows the event
stream for item state changes and for items added, removed, or updated. When
the stream goes silent for 30 seconds, the connection reports an error and
reconnects, waiting 1 second and doubling up to 30. After a reconnect it
reports each item whose state changed while the stream was down once, with its
latest state, marked caught up. Items that did not change are not reported,
and the first connect after the server starts does not count as an outage.

## Locations

Karakuri reads locations from openHAB's semantic model. It places an item at
its own location, else at the location of the
equipment it is a point of, else at the nearest location group among the
groups it belongs to. A location's floor is the floor it is part of. Item lists
group by floor and location, with items in no location last. A house without
semantic tags gets flat lists.

`GET /api/connections/{id}/openhab/items` and the MCP tool
`list_openhab_items` list items with type, state, whether each is read only,
and location and floor. `list_openhab_items` filters by `location` (a location
group's name or label, including the rooms on a floor), `type`, and text.
`send_openhab_command` sends a command to an item or group.

## Items as devices

**Settings, Devices, Add from openHAB** (or `discover_entities` and
`add_entities` with the openHAB connection) lists items by floor and location
with the device kind each becomes. None is ticked by default. Object IDs are
item names. To add items without picking them, including ones added later,
turn on auto import on the connection with a location or item type filter;
the Device auto import guide under **Help** covers it.

| Item type | Device kind | Commands |
| --- | --- | --- |
| Switch | Toggle | `ON`, `OFF` |
| Contact | Toggle labelled Open and Closed, read only | none |
| Dimmer | Light with brightness | a percent, `ON`, `OFF` |
| Color | Light with brightness and colour | `h,s,b` from the hex colour, a percent, `ON`, `OFF` |
| Rollershutter | Cover with position | position as `100 − position`, `UP`, `DOWN`, `STOP` |
| Number, Number:Dimension | Number with the item's minimum, maximum, and step, and its unit | the number |
| String with options | Dropdown of the options | the option |
| String | Text | the text |
| DateTime | Date and time | RFC 3339 |
| Player | Media player with play, pause, next, and previous | `PLAY`, `PAUSE`, `NEXT`, `PREVIOUS` |
| Location | Location with latitude and longitude, read only | none |
| Group with a base type | As its base type | sent to the group, which passes it to its members |

openHAB counts a rollershutter at 0 as open and a karakuri cover counts 100 as
open, so the device converts both ways. A colour's hue and saturation become
the hex colour, and its brightness becomes the light's brightness. A number's
unit, such as `°C`, becomes the device's unit. openHAB 4 and later report the
unit even before the item has a state. Older versions report it only inside
the state.

openHAB accepts commands outside an item's state description, such as a volume
past its maximum or a string outside its options. A device refuses them before
anything is sent: a value outside the description's minimum, maximum, or
options, and any write to an item openHAB marks read only. A device made from
a read-only item is a reading and takes no commands.

An openHAB device works like any device. The device's value changes when
openHAB reports the new state, never before. An item whose state is `NULL` or
`UNDEF` gives its device no value. A removed item marks its device missing
until an item of that name returns. openHAB treats a rename as a removal and an
addition, so add the renamed item again.

Groups without a base type, Image items, and Call items cannot be devices.
Trigger on a group's members instead.

## Nodes

Nodes are in the **openHAB** folder.

**openHAB Item Changed** starts a run when an item changes. It watches items
by name, every member of groups at any depth, or every item in or inside a
location, optionally narrowed by item type (`Number` matches dimensioned
numbers too) and by from and to states. The payload is `{item, type, label,
old, new, location}`, with `caught_up: true` for a change found after a
reconnect. **Ignore changes caught up after a reconnect** skips those. Group
and location membership is read at each change.

**openHAB Send Command** sends a command, templated from the message, to an
item or group. When openHAB refuses it, the node fails with openHAB's reason.

**openHAB Get Item** is a value node. It returns `{item, state, type, label,
location}` from the connection's current copy of the items.

Item fields complete against the connection's items, grouped by location.
