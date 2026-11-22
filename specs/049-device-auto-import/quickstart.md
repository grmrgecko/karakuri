# Quickstart: Device auto import

## Prerequisites

- `make dev`, then `systemctl --user restart karakuri-dev.service`.
- A local MQTT broker (for example `docker run -d -p 1883:1883 eclipse-mosquitto:2 mosquitto -c /mosquitto-no-auth.conf`)
  and `mosquitto_pub`.
- An MQTT connection to it in **Settings, Connections**.

## MQTT discovery auto import

1. Edit the connection: turn on **Auto import devices**, keep the discovery
   prefix `homeassistant`, save.
2. Publish a switch:

   ```sh
   mosquitto_pub -r -t homeassistant/switch/plug1/config -m \
     '{"name":"Plug","uniq_id":"plug1","~":"tasmota/plug1","stat_t":"~/POWER","cmd_t":"~/cmnd/POWER","avty_t":"~/LWT","pl_avail":"Online","pl_not_avail":"Offline"}'
   mosquitto_pub -t tasmota/plug1/POWER -m ON
   ```

3. **Settings, Devices** lists "Plug", marked auto imported, on. Toggling it
   publishes `ON`/`OFF` to `tasmota/plug1/cmnd/POWER`.
4. Delete it: the confirmation warns it will return. Publish the config again
   without `-r`: it returns.
5. The connection's import status shows one imported device. Publish a config
   with `{% if %}` in its `val_tpl`: it is listed as skipped.

## Removal

Unit tests drive removal with a test clock (`make test TARGET=server`); a
live check sets `remove_after` to `1h` and confirms the status shows the
setting.

## Picker

Turn auto import off, publish a second config, open **Add from MQTT
discovery**, and add it.
