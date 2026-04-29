<!--
Thanks for sending a PR! A few quick notes before we get into it:

- If this is your first contribution, please skim CONTRIBUTING.md.
- Branch names follow `feat/<name>`, `fix/<name>`, or `docs/<name>`.
- Keep PRs focused — one topic per PR makes review and revert easier.
-->

## What this PR does

<!--
One paragraph, plain English. What changes after this lands? If the
title already says it, expand on the *behavior* the user sees.
-->

## Why

<!--
One paragraph. What problem does this solve, or what use case does it
unlock? Link related issues with "Fixes #NNN" or "Refs #NNN".
-->

## Test plan

<!--
How did you verify this works? Bullet the concrete steps a reviewer
could repeat. "Ran the unit tests" is fine for pure refactors; bigger
changes need an end-to-end smoke. Mark items as you complete them.
-->

- [ ] `go build ./...` succeeds in `wizard/` and `mcp/`
- [ ] `go vet ./...` is clean
- [ ] Manual smoke: ran `localmind up` and exercised the affected path
- [ ]

## Screenshots / output

<!--
Optional. If this PR touches the WebUI, the wizard's terminal output,
or anything else a human looks at, paste a screenshot or a copy of the
relevant output here. Delete this section if it doesn't apply.
-->

## Checklist

- [ ] CI is green on this branch
- [ ] No new third-party dependencies added under `wizard/` (or, if added, justified above)
- [ ] Tests added for new behavior — **or** noted as deferred until `feat/tests` lands
- [ ] Docs updated for any user-facing change (`README.md`, `docs/*.md`, command help text)
- [ ] `CHANGELOG.md` has a line under `## [Unreleased]` describing the change

---

<sub>This template is auto-loaded from `.github/PULL_REQUEST_TEMPLATE.md`. Feel free to delete sections that don't apply to your change — the checklist above is the only part we'd like to see filled in for every PR.</sub>
