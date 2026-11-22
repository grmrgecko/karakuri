# The payload script

This script builds a device's command payload on the device, not on the dashboard. The server runs
it once per press. Every row that presses the device sends the same thing, and a viewer who cannot
write a script can still press it. The browser sends the interaction, and the server builds the
bytes.

## What it receives

| | |
| --- | --- |
| `field` | The attribute of the device's control kind being set, such as `on`, `brightness`, `position`, or `mode`. These are the names the state script returns. |
| `value` | The new value, already checked against the kind. A slider value outside its bounds never reaches the script. |
| `on` | The direction, for an attribute that has two. Other attributes leave it unset instead of guessing. |
| `previous` | What the device held before the press. |

A control with one value to set sends that value's key in `field`. A Toggle sends `on`; a Number,
Dropdown, or Text sends `value`; a Lock sends `state`. The
[state script](mqtt-device-state-script.md#the-keys-the-control-reads) lists the keys for every
kind, and the payload script receives the same names.

## What it returns

Return an object naming the topic to send on and the payload to send.

```javascript
return { topic: "home/lamp/set", payload: on ? "ON" : "OFF" };
```

The script names the topic, not a field on the device, because a device that takes more than one
kind of command usually takes them on more than one topic. `field` tells them apart, so a light can
take one attribute at a time.

```javascript
if (field === "brightness") {
  return { topic: "home/lamp/brightness/set", payload: String(value) };
}
return { topic: "home/lamp/set", payload: { state: on ? "ON" : "OFF" } };
```

The server sends a string payload as written and encodes anything else as JSON. The second branch
above sends `{"state":"ON"}`.

Brightness, cover position, fan percentage, and player volume arrive as percentages from 0 to 100,
whatever the device counts in, so convert them here.

```javascript
if (field === "brightness") {
  return {
    topic: "zigbee2mqtt/lamp/set",
    payload: { brightness: Math.round((Number(value) / 100) * 254) },
  };
}
```

Return nothing to send nothing. The press succeeds with no error and no message, and the row shows
nothing. An object with a payload but no topic does the same.

```javascript
// This device has no "off": the remote only sends the on code.
if (!on) return null;
return { topic: "cmnd/obk1F39F2A9/IRSend", payload: "Samsung 0x707 0x99 1" };
```

## QoS and retain

The server publishes a command at the device's **Command QoS** and retains it when **Retain
commands** is on. Both settings are under Delivery in the device editor. A retained command stays
on the broker, so a device that was asleep or rebooting picks it up when it reconnects. A
thermostat's set-point is the usual case.

Return `qos` (0, 1, or 2) or `retain` (`true` or `false`) with the topic to change either for one
message.

```javascript
if (field === "target") return { topic: "hvac/set/target", payload: String(value), retain: true };
return { topic: "hvac/set/" + field, payload: String(value), retain: false };
```

Any other value for either fails the press. A retained command stays until something replaces it.
To clear one, return an empty payload retained on the same topic.

```javascript
return { topic: "hvac/set/target", payload: "", retain: true };
```

## Buttons

A Buttons device declares a list of actions, each with its own label and value, and the script
reads the pressed action's value from `value`. One action draws as one button and several draw as a
row, which is how Up, Stop, and Down share one dashboard row. An infrared blaster needs only the
actions and one line of script.

| | |
| --- | --- |
| `On` | `Samsung 0x707 0x99 1` |
| `Off` | `Samsung 0x707 0x98 1` |

```javascript
return { topic: "cmnd/obk1F39F2A9/IRSend", payload: value };
```

The device keeps no state. Infrared is one-way, so nothing knows whether the television is on.

## Media players

A Media player device sets `field` to one of these:

- `state`: `playing`, `paused`, `idle` for stop, and `on` or `off` when the player has power
  control.
- `volume`, from 0 to 100.
- `muted`.
- `source`.
- `position`, in seconds.
- `command`, with `next` or `previous`.

Only the features turned on in the device's options arrive. This script drives a Squeezebox-style
bridge with one topic per action.

```javascript
const base = "squeeze/kitchen/set/";
if (field === "state") return { topic: base + {playing: "play", paused: "pause", idle: "stop"}[value], payload: "" };
return { topic: base + field, payload: String(value) };
```

`command` is a one-off press. The state script never sees it come back and should not return it.

## Testing it

**Test** in the editor opens the change the script receives, as JSON you can edit:

```json
{
  "field": "on",
  "value": true,
  "on": true,
  "previous": false
}
```

Running shows what the script printed and what it would send, so use `console.log` to see inside
it. The test publishes nothing. A script that sends nothing reports `{ "action": "none" }`, so you
can tell it chose to send nothing and did not fail.

## When it goes wrong

A script that does not compile, throws, runs out of time, or returns something that is not
`{topic, payload}` fails the press with the script's error message. The server publishes nothing.
Returning nothing is not a failure; see [What it returns](#what-it-returns).

## Notes

The script is JavaScript, run on the server in the same sandbox a script node uses, with no
network and no filesystem. It has 2 seconds per press unless the device's **Payload script limit**
says otherwise (100 to 10000 ms). A press waits on the script.

Leave this script empty for a device that only reports. Its dashboard rows then draw a reading
instead of a control, and the server refuses commands to it instead of publishing them.
