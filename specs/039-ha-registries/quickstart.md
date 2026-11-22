# Quickstart: Home Assistant areas and devices

The lab Home Assistant (`Home Assistant (10.0.10.5)`) is read only: its
registries are read, nothing is renamed or moved there, and no service is
called on it. Registry changes, area targeting, and device commands are
checked against the `hatest` fake in tests.

## 1. Registries and pickers

1. `get_ha_registry` on the lab connection lists floors, areas, devices, and
   labels with every registry `loaded` (or a stated reason).
2. `get_ha_states` with `area` set to one of its areas returns only that
   area's entities, each with `area` and `device`.
3. In the browser, an entities card row's entity picker groups suggestions by
   floor and area; typing an area name narrows to it. Screenshot; console
   check. Leave the dashboard unsaved.
4. A flow's call service node offers areas in its target picker. Leave the
   flow unsaved.

## 2. Area triggers (tests)

Against `hatest`: a trigger on an area and domain fires for a member's
change, ignores others, and includes an entity moved into the area by a
registry update event without re-arming; a reconnect rereads the
registries.

## 3. Home Assistant devices

1. `discover_entities` on the lab connection lists entities by area with
   their kinds; add one read-only sensor as a device named `HA device test`
   and see its value follow Home Assistant.
2. Commands to Home Assistant devices are checked only against `hatest`
   (light brightness, media pause, a refused write).

## 4. Card from an area

In the browser, **Add an area** on an entities card fills rows for one lab
area; screenshot, then discard the draft.

## Cleanup

Delete `HA device test` and any test flow or dashboard; report what was
created and removed.
