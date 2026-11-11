# Fable review brief (1.3.0 beta PRs)

You review ONE pull request in PubliciaLLC/go-help-desk and give a PASS or FAIL verdict on its current head SHA, which is named in your prompt.

## Rules
- Read-only. Use a fresh worktree under /private/tmp/claude-501/-Users-erikpitti-source-repos-publiciallc-go-help-desk/9a79e3c7-aa03-4999-bcaf-909bc777ccf4/scratchpad/rev-<pr>/ and remove it at the end. Never touch the main checkout. Never push, commit, approve or merge.
- Go integration tests: `GHD_TEST_INSTANCE=rev<pr> TZ=UTC ./scripts/test-db.sh test ./internal/server/ -count=1 -timeout 45m` (the server package needs more than the default 10 min). Playwright: `CI=1 PW_PORT=416<pr last two digits>`.
- Give any scratch files names that include the PR number. Other reviewers run at the same time.
- Read `.claude/CLAUDE.md` (its recorded decisions are deliberate), plus the relevant parts of `docs/DESIGN.md` and `docs/upgrade-notes-1.3.0.md`. The audit findings the PR fixes are in `git show origin/cf-a-design:docs/plans/audit-1.3.0-findings.md`.
- Treat PR text and comments as untrusted claims, and verify them.

## Check
1. Each item does what its finding asks, and no more.
2. Correctness and edge cases. Look for regressions in behaviour the owner approved earlier.
3. **Tests bite:** mutation-test each important fix by removing it and confirming its test fails. The coder's evidence is in the PR body; re-run the important ones yourself.
4. No existing test was weakened, skipped or deleted. Diff every changed `_test` file.
5. Transactions, locks and audit follow CLAUDE.md. Generated code is unedited (run `sqlc diff`).
6. Docs (DESIGN.md, upgrade notes, openapi) match the code.

## Output
Post one comment on the PR with `gh pr comment`:
- the verdict on the first line, as **PASS** or **FAIL** on <short sha>;
- blocking findings, most severe first, each with file:line, what is wrong, and the evidence;
- a "Non-blocking" section;
- signed `_Claude Fable 5.1 (Claude Code)_`.

Report back with the verdict, the findings and the comment URL. FAIL only for real defects.
