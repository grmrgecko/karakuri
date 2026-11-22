# Spec Kit source

This repository includes Spec Kit 1.2.4.dev0 from commit
`41a93d9a98c85bbc28b4694598119bd77beaa36d` of the
[grmrgecko/spec-kit](https://github.com/grmrgecko/spec-kit) fork. That commit is
[github/spec-kit](https://github.com/github/spec-kit) `main` at `9783b49e` plus
one fork change: `speckit-specify` updates an existing spec that owns the
described behavior instead of creating a feature, and generated success
criteria, quickstarts, and test tasks stay proportionate to what they verify.
The upstream MIT license is in [LICENSE.spec-kit](LICENSE.spec-kit).

The Specify CLI generated the Bash scripts, core templates, Codex and Claude Code
skills, integration manifests, and SDD workflow with:

```sh
specify init /tmp/karakuri-specify-scaffold --integration codex \
  --integration-options='--skills' --script sh --non-interactive --ignore-agent-tools
cd /tmp/karakuri-specify-scaffold
specify integration install claude --script sh
```

Codex is the default integration; Claude Code is installed alongside it. The
generated `.specify/`, `.agents/`, and `.claude/skills/` files were merged into
this checkout. `init-options.json` records `here: true` for this project. Assets
retain their generated contents except for the local scope changes below.
Karakuri's constitution, template overrides, and specifications are
project-owned. No Git, GitHub, or agent-context extension is installed. Core
scripts create spec directories and local feature state without changing
branches or rewriting `AGENTS.md`.

## Local scope changes

`scripts/bash/create-new-feature.sh` adds `--scope root|automation`, defaulting
to root `specs/`. Automation scope creates and numbers features under
`automation/specs/` using the same templates and shared feature pointer.
Existing feature selection already supports either path through
`SPECIFY_FEATURE_DIRECTORY`; common/planning/task scripts remain upstream.

The `speckit-specify` skill extends the fork's existing-spec check. It reads
`specs/README.md` and `automation/specs/README.md` and the affected specs, and
chooses between those roots by contract ownership when it creates a feature.
When a change alters behavior several specs state, it updates each one,
including prior specs a new capability changes. Removed IDs are not reused and
references to them are fixed. The plan skill revises an existing `plan.md` in
place rather than refilling the template. Both edits are applied identically to
the Codex skills under `.agents/skills/` and the Claude Code skills under
`.claude/skills/`.

The integration manifests record hashes of the installed assets, including the
patched files, so `specify integration status` reports no modified files.
Preserve these local changes when comparing a fresh scaffold. Spec, plan, and
task template overrides use repository-relative feature paths and carry the
fork's proportionate-test guidance.

Keep the source commit pinned when refreshing assets. To upgrade, generate
scaffolds from the installed commit and the new one, and three-way merge each
changed file into this checkout. Review manifests, local scope changes, and
overrides before committing. Preserve the constitution and both specification
trees.
