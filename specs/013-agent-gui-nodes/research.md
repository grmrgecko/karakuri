# Design research

- Decision: Compose a Desktop capability and proxy it through desktop helpers. Native Win32 calls in an agent service would otherwise run in session 0. Extending every accessibility backend with unrelated capture methods would mix concerns.
- Decision: Keep caller-side polling and shared snapshot code. Provider calls belong in helpers; long waits must retain caller cancellation and pin window identity.
- Decision: Preserve pure sources and add Screenshot Now and Window List Now. Existing value edges remain valid while execution nodes observe changes within one run.
- Decision: Give node evidence eight seconds total within the existing ten-second wrapper grace. Copying the CLI's two sequential ten-second evidence calls would discard useful results at the wrapper deadline.
- Decision: Automation Script uses the existing command execution machinery and explicitly configured CLI executable. An embedded second command interpreter would duplicate the CLI contract and enlarge this feature. The executable and its session setup are prerequisites.

- Decision: Extend existing selector and desktop DTOs, preserving unspecified defaults. Dedicated unmet-condition errors keep provider failures from becoming false results.
- Decision: Native controls and scoped dialogs use existing session/helper capabilities; filtered clicks use refreshed provider IDs.
- Decision: Inline command files contain literal source and are private temporary files. Rendering arbitrary input inside command text would let message contents become commands.
