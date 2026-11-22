# openHAB test server

`make openhab-up` starts openHAB 5.2.3 on http://localhost:19080 with the items
in `conf/items/karakuri.items`, creates the user `admin` with the password
`karakuri-test`, and writes an API token to `build/openhab-token`.
`make test TARGET=openhab` runs the tagged suite in this directory against it.
`make openhab-down` stops it and discards its data.

The items form a small semantic model: a ground floor with a living room and a
kitchen, a lamp equipment in the living room, and one item of each type the
connection maps (Switch, Dimmer, Color, Rollershutter, Number with and without
a unit, String with options, Contact, DateTime, Player, Location), plus a group
of lights and an item in no location.

The credentials exist only in the throwaway container.
