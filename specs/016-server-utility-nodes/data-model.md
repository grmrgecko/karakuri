# Data model: Server utility nodes

- Time window owns weekdays, timezone, optional timestamp path, coordinates and
  independent clock or solar endpoints. Clock strings use HH:MM; solar offsets
  must be within plus or minus twelve hours.
- Numeric config owns paths and one operation. Numbers must be finite; mapping
  input endpoints must differ; rounding precision must be an integer from -12
  to 12; units must share a dimension.
- Manager retains `integrations.Status` per connection ID. Entry timestamps change
  on transitions; repeated reports update error detail without changing Since.
- Driver generations rise before replacement or stopping. Older callbacks are ignored.
- Watcher queues hold at most 64 reports per subscription. Unsubscribe removes and
  closes the queue; manager shutdown closes every queue.

No database entities, schema changes or persistent formats are required.
