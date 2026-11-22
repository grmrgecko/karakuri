# Data model: Device auto import

## Connection config (encrypted, per kind)

| Key | Kinds | Meaning |
| --- | --- | --- |
| `auto_import` | all five | Add reported devices automatically. Default false. |
| `remove_after` | all five | Duration (`12h`, `7d`, `90m`); empty never removes. Minimum `1h`. |
| `discovery_prefix` | mqtt | Home Assistant discovery prefix. Default `homeassistant`. |
| `zigbee2mqtt_base` | mqtt | Zigbee2MQTT base topic. Default `zigbee2mqtt`; `off` turns Zigbee2MQTT import off. |
| `import_areas` | homeassistant | Comma-separated area IDs, names, or floors. |
| `import_domains` | homeassistant | Comma-separated entity domains. |
| `import_locations` | openhab | Comma-separated location item names, labels, or floors. |
| `import_types` | openhab | Comma-separated item types; `Number` matches dimensioned numbers. |

## mqtt_devices (new columns)

| Column | Type | Meaning |
| --- | --- | --- |
| `auto_imported` | bool, default false | Auto import created the device. |
| `discovery_key` | text, indexed | Home Assistant discovery identity: `<component>/<unique_id>`, or the config topic when there is no unique ID. |
| `last_reported_at` | timestamp, nullable | Last time the source reported the device. |

Uniqueness: one device per `(connection_id, discovery_key)` when the key is
set, enforced in `SaveDefinition`.

## auto_import_tombstones (new table)

| Column | Type | Meaning |
| --- | --- | --- |
| `id` | uuid | Primary key. |
| `connection_id` | uuid, indexed | Connection the device came from. |
| `source_key` | text | `z2m:<ieee>` or `ha:<discovery_key>`. |
| `removed_at` | timestamp | When removal happened. |

A tombstone exists for an MQTT-sourced device removed for not reporting. It
blocks import from retained replays and is deleted on live activity, which
imports the device again. Deleting the connection's auto import setting does
not clear tombstones; they are harmless when nothing reads them.

## Lifecycle

1. Source reports a candidate that passes filters, with no device and no
   tombstone: device created with `auto_imported = true`,
   `last_reported_at = now`.
2. Live activity or list presence while connected: `last_reported_at`
   advanced (flushed by the sweep).
3. Reference time older than `remove_after`: device and stored state deleted,
   event recorded, tombstone written for MQTT sources.
4. Author deletes the device: nothing written; the next report imports it.
5. Auto import turned off: devices remain, no longer swept.
