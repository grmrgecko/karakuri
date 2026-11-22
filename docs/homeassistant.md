# Home Assistant

A Home Assistant connection reads entity states, calls services, and follows
state changes over Home Assistant's WebSocket API. Karakuri also reads where
each entity sits, so flows can target rooms, and its entities can be added as
devices.

## Connecting

Add a connection of kind **Home Assistant** under **Connections**:

| Field | Meaning |
| --- | --- |
| Base URL | The instance's root URL, such as `http://homeassistant.local:8123`. |
| Access token | A long-lived access token from the Home Assistant profile page. |

Each time the connection signs in, including after a reconnect, it reads the
registries described below before it reports the state changes it missed, so
area and label triggers place those changes correctly.

## Areas, floors, devices, and labels

Karakuri reads Home Assistant's floors, areas, devices, entities, and labels.
A change made in Home Assistant, such as adding a device or moving an entity
to another area, reaches karakuri about two seconds after Home Assistant
finishes it. Karakuri writes nothing back. Manage areas, devices, and labels in
Home Assistant.

| Question | Answer |
| --- | --- |
| Which area is an entity in? | Its own area, or its device's area when it has none |
| Which floor? | Its area's floor |
| Which labels? | Its own and its device's. A label target also reaches every entity in an area that carries the label. |
| What is it called? | Its `friendly_name` |

Karakuri leaves out disabled entities and devices. It keeps hidden entities
but leaves them out of pickers unless asked for.

Home Assistant older than 2024.4 has no floors or labels. When a registry
cannot be read, karakuri leaves it empty and loads the rest. `get_ha_registry`
and `GET /api/connections/{id}/ha/registry` report each registry as `loaded`
or give the reason it is empty.

## Pickers

Entity pickers in flow nodes and in dashboard widget settings group entities
under floor and area headings and show each entity's device. A search matches
the entity ID, its name, its device, its area and floor, and its labels.
Entities in no area come last. The widget picker has a **Show hidden** switch.

Area, floor, device, and label fields complete against the registries and
store comma-separated registry IDs, which read back as names under the field.
Areas created in Home Assistant's interface have opaque IDs such as
`761b686f283b4b77b5dfdf1bf9e4b394` rather than `kitchen`, so pick them or look
them up with `get_ha_registry` instead of typing a name. You can also type a
`{{ }}` expression.

Over MCP, `get_ha_registry` lists floors, areas, devices, and labels with their
IDs. `get_ha_states` adds each entity's `area`, `floor`, `device`, and
`labels`, and filters by `area`, `floor`, `device`, or `label`, by ID or name.

## Entities as devices

**Settings, Devices, Add from Home Assistant** (or `discover_entities` and
`add_entities` with the Home Assistant connection) lists entities by floor and
area with the device kind each becomes. None is ticked by default. Object IDs
are entity IDs, and devices take the entity's friendly name. To add entities
without picking them, including ones added later, turn on auto import on the
connection with an area or domain filter; the Device auto import guide under
**Help** covers it.

The dashboard uses the same mapping to draw a Home Assistant entity, so a
device and a card row bound to the same entity show the same face and value:

| Domain | Device kind |
| --- | --- |
| light | Light, with brightness, colour, colour temperature, and effects as the entity supports |
| switch, input_boolean, siren, humidifier | Toggle |
| fan | Fan |
| cover | Cover |
| climate, water_heater | Thermostat |
| lock | Lock |
| media_player | Media player, with the features `supported_features` reports |
| number, input_number | Number |
| select, input_select | Dropdown |
| text, input_text | Text |
| scene, script, automation, button, input_button | Buttons, one press |
| person, device_tracker | Location, read only |
| anything else | Text reading, read only |

A Home Assistant device works like any device: dashboard rows, conditions,
history forwarding, the event log, and the Device Value, Command Device, Device
Changed, and Device Availability nodes. Karakuri checks a command against the
kind and the entity's features, then sends the matching service call, such as
`light.turn_on` with `brightness_pct` or `media_player.media_pause`. The
device's value changes when Home Assistant reports the new state, never before.
A device is offline while its entity is `unavailable` or `unknown`.

A device follows its entity. When you rename the entity ID in Home Assistant,
the device moves to the new ID. An entity that disappears marks its device
missing until it returns.

Karakuri reads kind options when the device is added. When the entity gains or
loses features in Home Assistant, edit the device's options, or delete the
device and add it again.

## Nodes

Nodes are in the **Home Assistant** folder.

**HA Call Service** targets entities, areas, floors, devices, and labels, in
any combination, and Home Assistant acts on all of them. When the call names no
entity and the registries are loaded, karakuri checks the target first. A call
whose areas, floors, devices, and labels hold no entity of the service's domain
fails with `no light entities in Hallway` instead of reporting success. A
service whose domain has no entities of its own, such as
`homeassistant.turn_off`, is not narrowed by domain.

**HA State** watches entities, or areas, floors, devices, and labels,
optionally narrowed by `domain` and `device_class`:

```json
{ "connection": "<home assistant>", "floor_id": "downstairs", "domain": "binary_sensor",
  "device_class": "motion", "to": "on" }
```

Karakuri matches each change against the registries as they are at that
moment. A sensor added to a downstairs room next month fires the trigger from
its first change, with no redeploy. The payload adds `area_id`, `area`, and
`device_id`. A target that matches nothing yet still arms and logs
`watching on Downstairs: no matching entities yet`.

**HA Get State** reads one entity, or, given an area, floor, device, or label,
every matching entity as `{states: [...], count}`.

Service calls from a dashboard name entities only. Karakuri refuses an area
target from a dashboard, because a dashboard's viewers are granted the entities
it shows, not the rooms they sit in.
