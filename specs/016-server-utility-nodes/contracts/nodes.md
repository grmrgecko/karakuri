# Node contracts

## Time window

Type `time-window`. Input `in`; outputs `inside`, `outside`, `error`.
Config: `weekdays` (comma-separated weekday names, empty means every day),
`location` (IANA zone), `time` (optional timestamp property), `latitude`,
`longitude`; each endpoint has `{start,end}_type` (`clock` or `sun`),
`{start,end}_time` (HH:MM), `{start,end}_event`, `{start,end}_offset`.
Default window is 08:00 to 18:00 every day. Inside/outside retain the message;
`meta.time_window_reason` explains an absent solar event. Weekdays select the
nominal start date and clock boundaries compare wall time.

## Numeric transform

Type `numeric-transform`. Input `in`; outputs `out`, `error`. Pure.
Config `source` and `target` default to `payload`; `operation` defaults to `scale`.
Scale uses `factor` (default 1), `offset` (default 0). Map uses `input_min`,
`input_max`, `output_min`, `output_max`, optional `clamp`. Clamp uses `min`, `max`.
Round uses `decimals`, `rounding` (`nearest`, `floor`, `ceil`, `truncate`).
Convert uses `from_unit`, `to_unit`. Inactive fields are ignored. Payload writes
emit number; nested payload writes emit object; metadata writes retain the
upstream payload type. Unaffected message properties survive.

## Connection status trigger

Type `connection-status`. Trigger with object output `out`.
Config `connection` selects one ID of any kind; `states` is a comma-separated
selection of connecting/connected/error/disabled (empty means all);
`emit_initial` defaults false. `test_params` can seed a sample.
Payload: `connection_id`, `connection_name`, `connection_kind`, `state`,
`previous_state`, `error`, `since` (RFC3339), `initial` (boolean). Topic is the
connection ID. Initial previous_state is empty. Emit only state changes, but
all observations advance the previous-state tracker. Standalone tests read
current status without subscribing unless a sample is given.
