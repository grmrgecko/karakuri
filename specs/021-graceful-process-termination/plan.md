# Design plan: Graceful process termination

**Feature**: `021-graceful-process-termination` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Add opt-in termination grace to agent Run Command and Run Script. Unix cancellation sends SIGTERM to the child's dedicated process group, waits for group disappearance or grace expiry, then sends SIGKILL. Keep immediate forced termination as the default.

## Technical context

Go standard-library process/context APIs and existing node metadata. Components are agent/exec and agent/agentnode; no dependency or persistent entity is added. Existing output limits and five-second WaitDelay remain. Grace is bounded to one minute.

## Constitution check

Pass before and after design. Cancellation/timeout remain failures and process nodes remain non-retryable. Existing drafts and catalog-driven settings remain valid. Host OS permissions still apply. No identity, database, CA, or encryption changes. Build/test through Makefile, verify through MCP and the editor, restart and verify the development service.

## Design and contracts

Share termination_grace metadata and validation through processPolicy. Validate duration and platform support before side effects. The Unix Cancel callback synchronously observes group existence with kill(-pid, 0), polling at most every 25 ms. ESRCH means complete; signal/check failures attempt forced termination and retain error context. Keep escalation active even after leader exit; do not schedule timers past execution. Windows rejects positive grace and keeps current forced cleanup.

The budget wrapper adds valid command/script grace before its existing ten-second margin, guarding duration overflow. Parent cancellation still returns immediately; Execute can continue bounded cleanup in its existing goroutine. The Automation Script catalog and behavior retain zero grace.

## State and data model

Only graph config changes: termination_grace is an optional duration string. Transient state is the cancellation callback's process-group identity and monotonic deadline. No migration or delayed timer lifecycle is required.

## Verification

Real POSIX fixtures prove cooperative marker writes, forced termination, normal completion, cancellation/timeout failure, and an ignoring descendant after leader exit with each stream mode. Serialization boundary tests cover duration limits and wrapper deadlines. Windows fixtures prove zero-grace launch and positive-grace rejection without changing output files. Run make dev/test for agent and server, cross-build agent for supported hosts, inspect MCP catalog and settings UI, then restart and verify the user service.

## Risks and open decisions

Children can escape a process group. Zombies may keep the group observable through the whole grace. Windows has no supported general cooperative mode. A canceled run can return before process cleanup finishes. No unresolved design decisions remain.
