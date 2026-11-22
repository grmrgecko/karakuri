# Tasks: Automatic updates

**Feature**: `specs/048-automatic-updates`
**Input**: [spec.md](spec.md), [plan.md](plan.md)

- [x] T001 Add `internal/update`: release detection, download and checksum verification, extraction, smoke test, binary and resource swap, pending record, recovery, confirmation, and the daily loop.
- [x] T002 Add the server's `update` configuration, template defaults, container overrides, `karakuri update`, and the serve loop, confirmation, and restart.
- [x] T003 Add the agent's `auto_update` and `update_prerelease`, `karakuri-agent update`, and the run loop, confirmation, and restart.
- [x] T004 Add tests, update the configuration and agent guides and the README, and check against GitHub.
- [x] T005 Add the supervised update and resume, the root update timer installed and removed with a system service, and the server loop's hand-off to it.
- [x] T006 Add supervised update tests, verify the rendered units, and update the configuration guide, the README, and this spec and plan.
