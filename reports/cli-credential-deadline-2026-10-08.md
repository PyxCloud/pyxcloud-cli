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

## Closed phase followup

The single official artifact attempt (run 37850888918, merge 17c8fd86) returned code timeout after 20.876 seconds, stdout 54 bytes, stderr zero, without watchdog termination. It supplied no component phase, so underlying cause remains UNKNOWN.

An additive `failurePhase` now preserves only credential_read, refresh, or status_http for the status command (generic transport uses http_request). Timeout/canceled codes and exit codes remain unchanged. JSON and human output contain no native message, path, URL, body or credential. Cancellation before a refreshed token is saved prevents late refresh side effects.

A race-enabled package check uncovered an existing test-fixture race in TestRunDoesNotTrustLedgerWhenBackendCheckIsFalse: HTTP handler counters were read by the test while the final timed-out handler could still write. Those counters now use atomics. The new HTTP fixture initially lacked staging endpoint configuration; using sandbox with an in-memory transport corrected the setup. Neither failure was deliberately introduced as a RED gate.

No further live credential or status attempt belongs to this source station. Repeat only after independent review and a new official CI artifact.

## Noninteractive native-read source followup

Observed official phased attempt: credential_read timeout at 20.173 seconds (merge 2c967337, official run 37851667480). Precise macOS condition remains UNKNOWN. The read now uses a SecItemCopyMatching query for the unchanged generic-password service/account, requests one data result, and sets kSecUseAuthenticationUIFail per query. It does not set kSecUseDataProtectionKeychain, migrate records, change ACLs, or change process-wide interaction policy. errSecInteractionNotAllowed becomes credential_access_required, exit 10, with a static action that stops automatic retries and preserves the stored session. Other native errors remain sanitized credential_store_unavailable. Native read context deadline remains the guard for service stalls independent of UI.

Apple documents that UIFail disallows user authentication and returns errSecInteractionNotAllowed when interaction is required: https://developer.apple.com/documentation/security/ksecuseauthenticationuifail . Apple marks this constant deprecated in favor of LAContext; the existing C/file-based Keychain implementation uses the per-query documented compatibility key instead of introducing a new Objective-C runtime or changing the store. Generic-password identity uses service/account: https://developer.apple.com/documentation/security/ksecclassgenericpassword . macOS search-list behavior: https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains .

Public signature evidence explains why replaced releases can request approval again. build-native-cli.sh uses codesign --sign -. Installed official binary has Signature=adhoc, TeamIdentifier=not set, designated requirement cdhash73a92d753ce7c5b47e680f182e40d53659a2e91c. Previous official binary requirement is cdhash14bca2b5375d802073566c146ceb528087370160. Same path and identifier do not preserve that identity across rebuilds. Native save uses SecKeychainAddGenericPassword with default access, with no stable explicit trusted-app issuer. Existing human ACL contents and lock state were not inspected.

Stable signing prerequisite: CLI repo secret-name inventory returned zero; workflow/scripts contain no Developer ID signing configuration. This is scope-limited evidence, not a claim that no certificate exists anywhere. No Keychain identity query, private-key read, signing downgrade, identifier-only spoofable ACL, or all-app access change was attempted. Seamless upgrade credential reuse requires a stable authenticated signing identity and an owned upgrade/ACL test; it is not claimed by the source-only UI fix.

No new native release, login, real credential read or live status command was run for this source followup. Native fixture remains opt-in and skipped; unit fixtures exercise static refusal propagation and record preservation.
