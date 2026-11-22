# Research: Graceful process termination

## Process-group cleanup

Decision: Perform cooperative termination, bounded group polling, and escalation synchronously inside the process cancellation callback.
Rationale: Go's context watcher waits for Cancel to return. WaitDelay only kills the direct process. A separate escalation timer canceled on parent exit can leave descendants alive, especially with file-only or discarded streams.
Alternatives considered: Parent-only signaling and a timer stopped after cmd.Run both fail the descendant contract.

## Windows support

Decision: Reject positive grace before launch on Windows.
Rationale: Process.Signal does not implement Interrupt there. Console control events require console association unavailable to general service agents. Non-forced taskkill is not a generic cooperative shutdown mechanism.
Alternatives considered: CTRL_BREAK and non-forced taskkill do not provide the required contract for arbitrary programs.

## Duration and deadlines

Decision: Use a duration string, zero default, and a one-minute maximum. Add valid grace to command/script wrapper budgets.
Rationale: Duration fields already exist in the catalog, fractional durations support short cleanup, and bounded grace prevents indefinite shutdown waits.
Alternatives considered: An unbounded grace would undermine execution deadlines; a positive default would change existing flows.

## Sources

- [Go os/exec contract](https://pkg.go.dev/os/exec)
- [Go context watcher and Wait implementation](https://go.dev/src/os/exec/exec.go)
- [Microsoft console control events](https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent)
