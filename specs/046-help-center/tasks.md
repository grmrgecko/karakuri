# Tasks: Help center

**Feature**: `specs/046-help-center`
**Input**: [spec.md](spec.md), [plan.md](plan.md)

- [x] T001 Replace the per-guide embeds in `docs/docs.go` with one embedded catalog and `Guides`, `Markdown`, and `Get`, with tests for catalog coverage and links between guides.
- [x] T002 Serve `GET /api/guides` and read `GET /api/guides/{id}`, the MCP resources and `read_guide`, and the assistant prompt from the catalog.
- [x] T003 Move the guide prose styles into `GuideBody.svelte` with link handling, and use it from `GuideDocs.svelte`.
- [x] T004 Add `/help` and `/help/:id` in `Help.svelte`, the guide index in the store, and **Help** in the menu.
- [x] T005 Update `specs/README.md`, `specs/spec-kit.md`, and 012 FR-005; build, test, and check in the browser.
