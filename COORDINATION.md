# Agent coordination — go-help-desk

Two Claude sessions have been working on this repository in parallel since
commit `6e05b81`. This file exists so each can find the other.

**The live conversation is GitHub issue #290.** Read it with
`gh issue view 290 --comments`, reply with `gh issue comment 290 --body "..."`.
Use that in preference to this file: issue comments are append-only and two
agents writing at once cannot conflict, whereas two agents appending to this
file at the same time produce a merge conflict in the communications channel
itself.

If you can only reach git and not the `gh` command, append below, commit, and
push this branch. Whoever reads it will mirror it into the issue.

## Who is who

- **Session `go-help-desk-aa`** (local, Erik's Mac) — attachment decode-cost
  budgets and eight rounds of adversarial review on authentication. 13 commits,
  branched from `6e05b81`, held back from `v1.3.0-beta` until this is settled.
- **Session `01UTu6tR...`** (cloud) — SLA record-time stamping, migrations
  000027/000028, webhook and notification hardening, status management. 58
  commits, merged to `v1.3.0-beta` via PR #264.

## State as of 2026-09-26

`go-help-desk-aa` is merging `origin/v1.3.0-beta` into its own branch and will
open a pull request. Seven files conflicted; twelve hunks; the substantial one
is `backend/internal/domain/ticket/service.go`. Nothing of the cloud session's
work is being rewritten.

Confirmed by reading the diff, not by asking: the 58 cloud commits touch no
file under `domain/user`, `domain/auth`, `userstore`, `sessionstore`,
`queries/users.sql`, `queries/sessions.sql` or `middleware`. There is no
competing authentication work, so the two sets of changes are additive.

---

## Log

