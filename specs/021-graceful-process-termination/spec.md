# Feature specification: Graceful process termination

**Feature**: `021-graceful-process-termination`
**Created**: 2026-10-06
**Status**: Implemented; verification limits recorded in quickstart.md
**Input**: Give Run Command and Run Script a configurable graceful termination period before forced termination on cancellation or timeout.

## User scenarios and testing

### User story 1: Allow cleanup before termination (Priority: P1)

An author gives a program time to flush files and perform cleanup when a run is canceled or the node times out.

**Why this priority**: Immediate forced termination prevents program cleanup.
**Independent test**: Real command and script fixtures record readiness, handle termination, and write a cleanup marker. Cancellation and timeout remain failures even when the program exits zero.

**Acceptance scenarios**:

1. Given positive termination grace on a supported host, cancellation or timeout requests cooperative termination once, allowing the program to finish cleanup before forcing termination.
2. Given no grace or zero grace, termination retains the immediate forced behavior.
3. Given a normal completion, no termination request is sent and the configured grace adds no wait.
4. Given invalid or unsupported grace, execution fails before launching or creating output files.

### User story 2: Bound cleanup of the whole process group (Priority: P1)

An author needs a canceled process and its descendants to stop even when they ignore cooperative termination.

**Why this priority**: Descendants can continue changing files after their parent exits.
**Independent test**: A process ignores cooperative termination, including a descendant whose parent exits first. Verify forced termination after the configured grace with returned, file-only, and discarded output.

**Acceptance scenarios**:

1. Given a program ignoring termination, the agent forces termination after the grace period.
2. Given a parent that exits while a descendant remains in its process group, the descendant still receives forced termination after the grace period.
3. Given node timeout, the outer execution deadline allows the grace period and existing final cleanup margin.
4. Given parent cancellation, the run stops waiting immediately while bounded process cleanup continues.

### Edge cases

- Descendants leaving the managed process group remain outside its termination guarantee.
- Exited descendants awaiting reaping may consume the full grace period.
- Windows service agents cannot provide generic cooperative termination; positive grace is rejected before launch.
- Grace does not turn a timeout or cancellation into success, even with accepted exit zero.
- Launch failures and successful normal exits must not leave delayed termination work.

## Requirements

### Functional requirements

- **FR-001**: Run Command and Run Script MUST expose a termination grace duration, defaulting to zero, accepting zero through one minute including fractional seconds.
- **FR-002**: Supported hosts MUST request cooperative termination of the managed process group on cancellation or timeout, then force termination of remaining group members after positive grace.
- **FR-003**: Zero or omitted grace MUST preserve immediate forced termination; normal completion MUST NOT wait for grace or schedule termination.
- **FR-004**: Invalid and unsupported grace MUST fail before process launch, script staging, or output-file changes.
- **FR-005**: Cancellation and timeout MUST remain failures independent of accepted exit codes and cleanup exit status; actions MUST NOT retry automatically.
- **FR-006**: The outer execution timeout MUST include configured grace plus the existing ten-second cleanup margin, with bounded arithmetic. Parent cancellation MUST still stop waiting immediately.
- **FR-007**: Cleanup MUST cover remaining process-group members when the leader exits first, independent of stream mode.
- **FR-008**: Catalog metadata MUST expose the option through existing server and editor settings. Existing flows, identities, databases, and encrypted secrets MUST remain readable.

### Key entities

- Termination policy: a node-local grace duration and the host's supported termination mechanism.
- Managed process group: the launched program and descendants retaining group membership.

## Success criteria

- **SC-001**: Command and script cleanup fixtures write their final markers before termination with positive grace and retain failure outcomes on cancellation/timeout.
- **SC-002**: Ignoring fixtures cannot continue producing output after grace and final cleanup, including when their parent exits first.
- **SC-003**: Normal exits incur no grace delay; existing zero-grace flows preserve termination behavior.
- **SC-004**: Invalid and unsupported settings leave launch markers and pre-existing output files untouched.

## Assumptions and scope

Grace is opt-in, at most one minute, and applies to Run Command and Run Script only. Unix hosts use their managed process group; Windows retains immediate forced termination and rejects positive grace. This adds no credential, identity, database, or encryption migration. Tests exercise real processes/files through Makefile targets.
