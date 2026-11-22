# Quickstart: Media control kind

The lab Home Assistant is read only: Home Assistant commands are checked
against the test fake in the widget tests and by reading service-call requests,
not by pressing a real player. Test devices use the `karakuri-test` base topic
on the lab broker.

## 1. Home Assistant player on a card

1. Find a `media_player` on the lab Home Assistant with `get_ha_states`.
2. Place an Entities Card row and an Entity Control bound to it on a test
   dashboard; the row shows its state, title, and the controls its
   `supported_features` allow. Screenshot; console check.
3. Do not press anything on the real player.

## 2. Media control and MQTT media device

1. Create a media control `Media test`; set state, volume, source, and a
   `next` command from the controls panel and with MCP `set_control`; a volume
   of 120 and an unlisted source are refused.
2. A flow with Control Changed on the control records the `next` command; a
   later read of the control has no `command`.
3. Create an MQTT device of kind media under `karakuri-test/mediatest`; publish
   a state with title, artist, position, and volume; the card shows them and
   the progress advances. Press pause and next from the card and capture the
   payload script's publishes with `capture_mqtt_messages`.

## 3. Now Playing

Bind Now Playing to the media control and to the MQTT device; screenshot each.

## Cleanup

Delete the test control, device, flow, and dashboard; clear the
`karakuri-test` topics; report what was created and removed.
