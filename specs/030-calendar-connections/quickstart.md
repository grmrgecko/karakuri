# Quickstart: Calendar connections and event triggers

Build with `make dev`, test with `make test TARGET=server` and
`make test TARGET=frontend`, then `systemctl --user restart
karakuri-dev.service`. Use MCP with the development token. Name test objects
`030 …` and remove them afterwards.

A local ICS file served from the repository's test server is enough for most
checks; edit it to move events. CalDAV uses a disposable Radicale container on
127.0.0.1. Google uses a public holiday calendar with an API key when one is
available, and is otherwise covered by recorded responses in tests.

## 1. Connections (US1, SC-005)

1. Create a `calendar-ics` connection to the local feed and test it. Expect
   the detail to count the next week's events.
2. Start Radicale, create a calendar with two events, add a
   `calendar-caldav` connection, and test it. Expect the detail to list the
   calendar names.
3. Read both connections over REST and MCP. Expect secrets masked.

## 2. Start and end across a restart (US2, SC-001)

1. Add an event starting in two minutes and ending in four. Arm a trigger with
   `edges: both` feeding a control-set node.
2. Restart the service between start and end.
3. Expect one start run and one end run, each within a minute of its moment.

## 3. Missed moments (US2, SC-002)

Stop the service across an event's start, once with `missed_deadline:
deliver` and once with `drop` and a short grace. Expect a late run with
`meta.late_ms`, then a canceled run naming the dropped occurrence.

## 4. Recurrence (SC-003)

Use a weekly event with one cancelled occurrence and a fake clock in the
plugin tests over three weeks. Expect a firing for every other occurrence.

## 5. Lookup (US3)

With one event in progress and two later today, run `calendar-events`.
Expect one entry in `in_progress` and two in `upcoming`.

## 6. Migration (US4, SC-004)

The development dashboard's calendar widget reads two inline ICS feeds. After
the upgrade, expect two `Calendar: …` connections, the widget showing the
same events with the same names and colours, and no URL credentials or API
keys left in `settings`.

## 7. House mode (US5)

Build the flow from the guide against a test event and check the mode control
changes on start and returns on end, including two overlapping events.
