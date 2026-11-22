# Data model: Graceful process termination

## Node config

termination_grace is an optional string duration. Empty or omitted means zero. The duration must be between zero and one minute inclusive; fractional seconds are supported. Windows accepts only zero.

The field belongs to Run Command and Run Script, alongside existing process policy fields. No ports, persisted output shape, identity, or database schema changes.

## Transient cleanup

A managed Unix process group has its leader PID, a cooperative termination request, and a monotonic grace deadline. Normal completion creates no delayed work. Cancellation cleanup ends when the group disappears or after forced termination is requested at the deadline.
