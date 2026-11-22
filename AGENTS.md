# Karakuri

Karakuri is a reliability-focused home automation flow engine and dashboard with
retry policies, failure routing, MQTT, Home Assistant, a Svelte node-graph editor,
and a dashboard editor and renderer.

Before changing a feature, read `.specify/memory/constitution.md`, the feature's spec
and plan (indexed in `specs/README.md`), and its guide in `docs/`. Follow the Spec Kit
workflow in `specs/spec-kit.md` and update the spec, plan, and guide with any behavior
change. Guides in `docs/` explain setup and use for people; they never cite specs or
describe internals. Spec Kit does not authorize branches, publishing, or unrelated
changes.

A change to stored data, configuration, flow or dashboard JSON, export files, REST or
MCP interfaces, or the plugin protocol must keep existing installations working or ship
a migration. Databases, enrolled agent identities, and secrets encrypted with
`security.encryption_key` are not regenerable, so call out any change that breaks them.

# Code style details

- Write pragmatic, systems-oriented Go focused on correctness and clarity.
- Favor explicit control flow and simple data structures over abstraction.
- Use short, consistent receiver names.
- Exported names are clear and descriptive; internal helpers are lowerCamelCase.
- Return errors when callers can act; otherwise log and continue safely.
- Log operational details at debug level, lifecycle events at info, and failures at error level.
- Avoid panics, hidden side effects, and over-engineering.
- Comments are functional and intent-focused, explaining why something exists or what role it plays.
- Exported types and methods have short, direct doc comments that describe responsibility.
- Comments are operational in tone, written for someone maintaining or debugging the system.
- Inline comments are brief and precise, explaining non-obvious logic or marking logical phases of a function.
- Functions with multiple logical steps use short section comments to label each phase (e.g. "Validate input.", "Persist to database.", "Build response.").
- No conversational language, jokes, or speculative notes—comments are concise and purposeful.
- The code is expected to remain readable without comments; comments add clarity where reasoning is not immediately obvious.
- The name of the element (func, struct, var, ect) being commented should be the first word in the comment.
- Comments are expected to be a complete sentence with ending notation such as a period. We are humans with proper english.

# Build, test, and commit

- Build with `make dev` and test with `make test`; `TARGET=server`, `frontend`,
  `agent`, `auto` (build), or `automation` (test) limits them to one component. Never
  invoke compilers or test runners directly.
- A full or server build installs the user unit `karakuri-dev.service`. After building,
  run `systemctl --user restart karakuri-dev.service`; never run `karakuri serve`.
- Before handing off, check the unit is active and http://localhost:8080 answers, and
  leave it running. Logs are in `build/var/karakuri-dev.log`. Development login is
  `admin` / `password123`.

# Feature checks

- Prefer MCP at `http://localhost:8080/mcp` for functional checks. From `build/`,
  `./karakuri token list admin --name=mcp --raw` prints the development token, and
  `./karakuri token create admin mcp` makes one if none exists. See `docs/mcp.md`.
- Use the browser when a change affects the UI, and always for canvas changes, whose
  client-side derivations MCP and Go tests cannot see. Change the settings that drive
  derived behavior and confirm the node updates live, take a screenshot after each
  interaction once the canvas settles, and check the console before calling it done.
- Leave drafts unsaved when the check is only visual, and report any test flows added
  to the development database.

