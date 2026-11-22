# The state script

An MQTT device is the topics it listens on plus this script, which reads them. The server runs the
script once per message, and what it returns is what the device knows. You can add Zigbee2MQTT
devices with the script written for you; see [Zigbee2MQTT devices](#zigbee2mqtt-devices). ESPHome
nodes need no script. The ESPHome guide covers adding their entities as devices the node reads and
commands, and the Frigate guide covers adding a Frigate server's camera switches the same way.

## What it receives

| | |
| --- | --- |
| `topic` | The concrete topic this message arrived on, not the filter that matched it. |
| `payload` | The message body as text. |
| `meta.binary` | `true` when the body was not text. `payload` is then base64. |
| `meta.state` | Everything the device knows so far, for a script that works out the new value from the old one. |
| `meta.retained` | `true` when the broker delivered this message from its retained store as the device subscribed. `false` when it was published while the device was listening. |

`binary`, `state`, and `retained` are also plain variables, so you can leave off `meta.`.

## What it returns

Return an object holding only the values this message carried. Any key you leave out keeps the
value the device already had.

A device that reports its brightness in one message and its colour in the next says nothing about
brightness in the second. Replacing the whole state with each message would lose the brightness.

```javascript
const d = JSON.parse(payload);
return d;
```

Return nothing to ignore the message. A device watching a wildcard needs this, because not every
topic under it is worth reading.

```javascript
if (topic.endsWith("/lwt")) return null;   // not for me
```

Returning anything other than an object or nothing fails the message. The device reports the
failure instead of storing a value it cannot use.

## The keys the control reads

A device names a control kind, and that kind decides which keys a dashboard draws it from. Return
those keys and every widget showing the device draws it correctly with nothing else configured. The
editor lists the keys your kind uses under this script.

| Control | Keys |
| --- | --- |
| Toggle | `on` |
| Number, Dropdown, Text, Date and time | `value` |
| Buttons | none; a set of actions has no state behind it |
| Lock | `state`: `locked`, `unlocked`, `locking`, `unlocking`, or `jammed` |
| Light | `on`, `brightness`, `color`, `color_temp`, `effect` |
| Cover | `state`: `open`, `opening`, `closed`, `closing`, `stopped`; plus `position` and `tilt` |
| Thermostat | `mode`, `target`, `current`, `action`, and `fan_mode` |
| Fan | `on`, `percentage`, `preset`, `oscillating`, `direction` |
| Location | `state`: `home`, `not_home`, or a zone name; plus `latitude`, `longitude`, `accuracy` in metres, and `source` |
| Media player | `state`: `off`, `on`, `idle`, `playing`, `paused`, or `buffering`; plus `volume`, `muted`, `title`, `artist`, `album`, `art` (a URL), `position` and `duration` in seconds, `position_at`, `source`, and `sources` |

Brightness, cover position, fan percentage, and player volume are percentages from 0 to 100,
whatever the device counts in. A Zigbee light that counts to 254 converts here, so every light on a
wall panel reads the same way. `color` is a hex string such as `#ff8800`, and `color_temp` is in
kelvin. A player's `position_at` is the RFC 3339 time at which `position` was measured. Leave it out
and the dashboard counts from when the position or the play state last changed, which drifts if
the player reports rarely.

```javascript
const d = JSON.parse(payload);
return {
  on: d.state === "ON",
  brightness: Math.round((d.brightness / 254) * 100),
};
```

The device keeps any other key you return, and a dashboard can show it on a row of its own. That is
how a battery level or link quality sits beside the light it belongs to.

## A phone over OwnTracks

OwnTracks publishes to `owntracks/<user>/<device>`. Make a device of kind Location on that topic
with this script. Region enter and leave messages set the zone, and location updates move only the
coordinates, so a GPS fix near a region boundary does not flip the zone.

```javascript
const m = JSON.parse(payload);
if (m._type === "transition") {
  if (m.event === "leave") return { state: "not_home" };
  const zone = String(m.desc || "");
  return { state: zone.toLowerCase() === "home" ? "home" : zone };
}
if (m._type === "location") {
  return { latitude: m.lat, longitude: m.lon, accuracy: m.acc, source: "gps" };
}
return null;
```

Name the region at home `Home` in the app. Any other region's name becomes the zone as written, so
list it in the device's zones to give it a label. A leave sets `not_home` even when the phone is
still inside another region, so nest regions only if a flow sorts that out. Karakuri sets `since`
itself when `state` changes and drops a `since` the script returns.

## Availability

Every device is online, offline, or unknown. The device list shows it, dashboards draw a device
that is not online as unavailable, and the server keeps it with the device's state across restarts.
These decide it:

- The connection. While the broker is down, every device on it is unknown.
- An availability topic, usually the firmware's last-will topic. The device follows its online and
  offline payloads, which default to `online` and `offline`. A JSON object whose `state` is one of
  them also matches, which is how Zigbee2MQTT sends it. Matching is exact, so Tasmota's `LWT`
  needs `Online` and `Offline`. Any other payload changes nothing, and the device shows it. After
  the broker comes back, a device with a topic is unknown until the topic speaks. A retained
  last-will message does that at once.
- An expected reporting interval (`expect_every_s`, at least 60 seconds), for firmware whose
  last-will never fires. A device that sends nothing for longer is offline until its next message.
  Any message counts, including ones the script ignores. The server checks every 15 seconds.
- Otherwise the device is online whenever its connection is up. Removing the topic or the interval
  from a device drops what it had decided, so a rule the device no longer has cannot leave it
  offline.

The server refuses a command to an offline device and reports when it went offline. The exception
is a device set to send commands while offline, for a sleeping battery device that picks up queued
commands when it wakes. Commands to an unknown device go through. The Device Availability trigger
starts a run when a device, or any device, changes availability.

## Zigbee2MQTT devices

**Add from Zigbee2MQTT** in the device settings, or the `discover_zigbee2mqtt_devices` and
`add_zigbee2mqtt_devices` MCP tools, read the device list Zigbee2MQTT retains on
`<base>/bridge/devices`. The base topic is `zigbee2mqtt` unless yours differs. Reading publishes
nothing. The list shows each device's model, power source, the kind it would be added as, and the
other values it reports. It leaves out the coordinator, and you cannot add a device that is
unsupported, still being interviewed, disabled, or already added. With auto import on the MQTT
connection, every Zigbee2MQTT device and every device announcing itself with Home Assistant MQTT
discovery is added without picking it; the Device auto import guide under **Help** covers it.

An added device is an ordinary MQTT device. Its kind comes from the device's main capability: a
light, switch, cover, lock, fan, or thermostat first, then a contact, occupancy, leak, or similar
sensor, then a button's last `action`, then the first reading Zigbee2MQTT does not mark as a
setting or a diagnostic. The state keeps every other property, such as battery, power, energy, and
link quality, under its own name, ready for a row of its own. The generated state script converts
brightness to a percentage, mireds to kelvin, and xy colour to hex. The generated payload script
publishes to `<base>/<ieee address>/set`, which Zigbee2MQTT accepts in place of the friendly name.
The availability topic is `<base>/<friendly name>/availability`.

Turn on availability in Zigbee2MQTT's settings for its devices to report offline. Without it, set
an expected reporting interval on the device instead.

Added devices follow Zigbee2MQTT. A rename moves the state and availability topics that use the old
friendly name and keeps the karakuri name, the scripts, and topics you added. A device whose
availability topic you cleared follows a rename made while the server is running. When a device
leaves the list, karakuri marks it missing and shows it unavailable until it returns. It never
deletes the device. **Regenerate** on the device, or `regenerate_mqtt_device`, rebuilds the kind,
options, scripts, and topics from Zigbee2MQTT's current description, replacing your edits to them.
A device with a second switch or socket gets the first as its control and the second as a field.
To command the second one, add another device by hand.

## Several topics

`topic` lets one device read everything it publishes, across as many topics as it uses.

```javascript
if (topic.endsWith("/brightness")) return { brightness: Number(payload) };
if (topic.endsWith("/color")) return { color: payload };
if (topic.endsWith("/state")) return JSON.parse(payload);
return null;
```

Declare the topic as `home/lamp/#` and this one script covers all of them.

## Working from the last value

`meta.state` is what the device holds right now, which a running total or a delta needs.

```javascript
const { state } = meta;
return { total: (state.total || 0) + Number(payload) };
```

## Binary payloads

A body that is not valid text arrives base64-encoded with `meta.binary` set. The sandbox has no
`atob` or `Buffer`, so a device that sends bytes decodes them in the script.

```javascript
function decodeBase64(text) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  const bytes = [];
  let bits = 0;
  let value = 0;
  for (const c of text.replace(/=+$/, "")) {
    value = (value << 6) | alphabet.indexOf(c);
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((value >> bits) & 0xff);
    }
  }
  return bytes;
}

if (meta.binary) {
  return { level: decodeBase64(payload)[0] };
}
```

## Retained messages

A broker keeps the last retained message on a topic and hands it to every new subscriber. That is
how a device that reports only changes still has a value after karakuri restarts. A device that
reports events should not replay them. A bridge that publishes button presses retained would replay
yesterday's press every time karakuri reconnects. Skip those messages with `retained`.

```javascript
const d = JSON.parse(payload);
if (retained && d.action) return null;   // a replayed press, not a new one
return d;
```

## Testing it

**Test** in the editor opens the message the script receives, as JSON you can edit:

```json
{
  "topic": "zigbee2mqtt/lamp",
  "payload": "{\"state\":\"ON\",\"brightness\":128}",
  "retained": false
}
```

Set `"retained"` to `true` to test a message the broker replayed. Add a `"state"` object to stand
in for what the device already holds, which is what `meta.state` reads. Running shows what the
script printed and what it returned, so use `console.log` to see inside it. The test subscribes to
nothing, changes no state, and leaves the running device untouched. A script that ignores the
message reports `{ "action": "ignored" }`, so you can tell it chose to ignore the message and did
not fail.

## When it goes wrong

A script that does not compile, throws, runs out of time, or returns something other than an
object fails the message. The device's state does not change, so the last good values stay. The
server logs the failure once, not once per message, and sends it to any dashboard watching the
device.

## Notes

The script is JavaScript, run on the server in the same sandbox a script node uses, with no
network and no filesystem. It has 250 ms per message unless the device's **State script limit**
says otherwise (50 to 5000 ms). It runs for every message on every topic the device declares, so
keep it to reading, and raise the limit only for a device whose messages are large, such as an
energy meter's full document.

A device asks the broker for its **State QoS** (0, 1, or 2) on its topics and availability topic.
A broker may grant less, and the device editor shows when it did.

A device listens from the moment you save it, whether or not a dashboard is open. A device that
reports only what changed cannot be caught up later.

The server stores a device's merged state at most every five seconds and restores it when the
server starts. Until the device reports again, the restored values are stale. A device read through
REST or MCP carries `stale` and `reported_at`, the `mqtt-device-get` node adds `meta.stale` and
`meta.reported_at`, and dashboards label the value as last known. Restoring is not a report, so
`mqtt-device-changed` does not fire for it, and `meta.state` in the next script run is the restored
state. Changing a device's control kind discards its stored state, since values decoded for one kind
may not fit another.
