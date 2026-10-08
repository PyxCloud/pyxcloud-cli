# CLI credential deadline station

Stable batch item CLI-DEADLINE-114; source 2907c0d043b2629612078b5808d987ecf98c2785; procedure v1. No live credential reads, login retries, browser tokens, journey writes, or API inference were performed. API billed cost is unknown; this station made no inference calls.

## Evidence and boundary

The previous staging project 114 status attempt was terminated after more than 20 seconds with no output. Its precise blocking component remains UNKNOWN. The official source does establish an independent defect: status creates a command deadline, transport requests a token before HTTP, cachedTokenAccess calls Store.Load synchronously, and the native implementation calls SecKeychainFindGenericPassword without a context. HTTP's 30-second timeout does not bound that pre-request native read. The wrapper has no subprocess timeout; CLI defaults to two minutes unless --timeout is supplied.

The change waits for credential reads through the command context and caches cancellation as the command's failed load. A read that later returns cannot refresh, save, or send its token. It does not cancel Security.framework itself. It bounds the command response and permits process exit; a library caller keeping the process alive may retain one native goroutine until the OS call returns.

## Numbered protocol

1. Measure without credential access: `git rev-parse HEAD`; verify official install receipt source and artifact SHA.
2. Act only on the isolated source: context-bound token read, one per command. Never replace the installed recording artifact with a local build.
3. Verify: `go test -race ./internal/passocli -run 'TestTokenCache|TestLoginHelp|TestExecutePreservesWatchRecordsBeforeFinalTimeoutError' -count=1`; `go test ./internal/passoauth ./internal/passotransport`; `git diff --check`.
4. Release through normal reviewed official CLI CI before any runtime retry. Future authorized read: `python3 artifacts/specops-journey-videos-2026-10-07/local-agent/launch-owned-session.py -- passo --profile staging --project 114 --timeout 20s status`. Use the same final artifact for the recording.

## Fail-closed / STOP

Missing final official artifact or check evidence: keep station pending. Credential-store denial: stop retries. Unknown timeout phase: preserve UNKNOWN; do not label Keychain as the observed cause. Late credentials: discard without downstream effects. Source/artifact changes: invalidate recording continuity.

| Common mistake | Required action |
| --- | --- |
| HTTP timeout is assumed to include token retrieval | Trace pre-request credential acquisition |
| Native timeout means native call canceled | State response bound and OS-call limitation |
| Re-login or repeatedly reopen Keychain to diagnose | Use source and disposable fixture evidence |
| Local patched executable counts as official | Wait for reviewed official release artifact |

Acceptance: all three commands in step 3 PASS on the edited source. The passocli selection ran with race detector; passoauth native human fixture remains opt-in and was not run. Accepted output is one source fix, pending independent review and official release. No live journey acceptance is claimed.
