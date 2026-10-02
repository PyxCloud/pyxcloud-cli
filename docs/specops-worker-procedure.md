# Luna atomic worker procedure

## Exact commands
Use the absolute worktree provided in the brief as the working directory.
```sh
git status --short --branch
git rev-parse HEAD
go test ./internal/PACKAGE
go test ./...
go build ./...
git diff --check
git diff --stat
```
The coordinator substitutes PACKAGE and supplies exact test names before dispatch. Backend tasks receive their own package checks and shared-DB instructions; do not run broad backend tests that can launch independent Postgres containers.

## Numbered protocol
1. Measure: confirm branch/base commit and allowed files; run the named baseline check.
2. Act: write the specified behavioral test, observe the expected failure, implement only the brief.
3. Verify: run the named checks; inspect the diff and ensure no out-of-scope files or secrets.
4. Commit only allowed files; report commit, checks, failures and blockers in at most 15 lines.
5. Coordinator reviews the task; only then may dependent work start.

## Fail-closed contract
PASS requires passing named checks and the specified observable behavior. Missing contracts, credentials or capabilities are BLOCKED, never synthesized success. Do not push, merge shared branches, spend on product models, start cloud resources, or acquire the shared sandbox from a worker.

## Red Flags — STOP actions
- Baseline differs from brief: report the observed commit and stop editing.
- Interface absent or contradictory: report the exact symbol/route and stop; coordinator supplies the ruling.
- Unexpected test failure: return command, bounded error and diff; do not improvise an unrelated fix.
- File outside allowlist needed: stop and request an amended brief from coordinator.
- Credential material in output: stop output collection and report the source without repeating values.

## Common mistakes
| Shortcut | Required action |
|---|---|
| Endpoint looks plausible | Inspect its registered route and handler first |
| Fake is green, cloud is done | Record provider mode and keep real acceptance NOT RUN |
| Catch every error and proceed | Return the typed failure and stop the stage |
| Re-run a mutation after timeout | Reconcile authoritative state before retry |
| Print full payload for debugging | Use bounded metadata; no secrets in logs |
| Open another sandbox | Use the coordinator-owned shared stack |
| Think harder about an unclear brief | Report the missing decision; coordinator resolves it |
