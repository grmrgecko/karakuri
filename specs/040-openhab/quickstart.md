# Quickstart: openHAB connection

Everything runs against the disposable openHAB in Docker; no other openHAB is
touched.

## Setup

```sh
make openhab-up          # openHAB 5 on http://localhost:19080, token in build/openhab-token
make test TARGET=openhab # tagged suite against it
```

## 1. Connect and list

1. Over MCP, create a connection `openHAB test` of kind `openhab` with
   `base_url` `http://localhost:19080` and the token; test it: version and
   item count, authenticated.
2. `list_openhab_items` with `location: Kitchen` lists the kitchen switch,
   blind, and temperature.
3. A wrong token tests as unauthorized.

## 2. Items as devices

1. `discover_entities` lists the items by location; add `LivingDimmer`,
   `KitchenBlind`, `KitchenSwitch`, and `KitchenTemp`.
2. `command_mqtt_device` brightness 40 on the dimmer and position 30 on the
   blind; openHAB's states read `40` and `70`.
3. Set `KitchenTemp` to `21.5 °C` through openHAB's REST API; the device
   shows 21.5 with unit °C within a second.
4. A volume of 70 on `Volume` (maximum 50) is refused before reaching
   openHAB.

## 3. Flows

A flow with an item trigger on location `Kitchen`, type `Switch`, to `ON`, into
a debug node; send `ON` to `KitchenSwitch` and see one run. A Send Command
node sends `OFF` to group `gLights`.

## 4. Catch-up

With the trigger armed, `docker restart` the container after changing nothing,
then change an item through the console while the connection is down
(`docker pause`, REST unreachable): on resume, one caught-up run.

## 5. Browser

A card with the dimmer and blind devices; screenshot; console check.

## Cleanup

Delete the test connection, devices, flow, and dashboard; `make openhab-down`;
report what was created and removed.
