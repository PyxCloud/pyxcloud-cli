# macOS authentication during a Passo journey

Use one reviewed official `passo` executable at one stable path for login and the entire journey. Do not alternate between rebuilt binaries, `./passo`, credential inspection helpers, or the `security` command against the real credential. A command caches its credential in memory, loads Keychain once, and refreshes it when needed. Concurrent requests share that refresh. A separate command process must load its credential again; there is no cross-process token file or authentication bypass.

## Measure → act → verify

1. Record the executable's identity without accessing credentials:

   ```sh
   shasum -a 256 /absolute/path/to/passo
   codesign -dv --verbose=4 /absolute/path/to/passo 2>&1
   codesign -dr - /absolute/path/to/passo 2>&1
   ```

2. Keep that exact executable unchanged for the journey. Use its normal `login` command with the selected profile and normal browser SSO. If macOS requests Keychain access, verify that the named application is this executable. Approve only that application's access through the normal macOS dialog when intended. Never grant access to all applications or weaken Keychain protection.

3. If access was denied, stop automatic retries. Resolve the exact application's normal Keychain access interaction, then retry the command once. A failed credential load remains failed for the current command process, preventing repeated dialogs while polling. `authentication unavailable` is intentionally redacted and is not proof that the token expired. Failed credential saving also fails closed.

4. Verify one bounded authenticated read with the same executable. Do not launch additional credential-reading helpers to verify it. Preserve backend authorization: a successful login does not establish access to every project.

## Red flags — STOP

- An unexpected application requests access: cancel and inspect its executable identity.
- Repeated dialogs: stop polling/rebuilding and verify that only the frozen executable is running.
- A proposed fix grants all applications access, copies tokens to public files, or passes credentials in arguments: reject that fix.
- A release claims stable signed identity without a valid production signing identity: stop that claim.

## Actionable failures

A failed native credential operation returns exit10 and `credential_store_unavailable`. Human output explains the exact-application recovery step; JSON provides `nextAction.key=resolve_credential_store` and `automaticRetry=false`. The command performs no API request after a failed load and never starts another login automatically. Native error details are discarded, so this state does not assert that login expired or distinguish a locked, denied or missing item.

## Signing limitation

Ad-hoc signing verifies the current artifact's integrity. Its designated requirement can depend on its code hash, which changes after rebuilding; a fixed path or identifier alone does not establish trust across releases. Stable production identity requires a genuine issuer-backed signing identity and a reviewed signing pipeline. Do not manufacture a globally trusted certificate or substitute an unrestricted ACL. Current local metadata reported no valid code-signing identities; production signing remains an explicit prerequisite, separate from native framework support.

| Common mistake | Correct action |
|---|---|
| Rebuild or swap binaries during a journey | Freeze one reviewed artifact before login |
| Verify credentials through several helpers | Verify a bounded API read through the same executable |
| Interpret every failure as expired login | Preserve redacted errors and inspect executable identity first |
| Repeatedly retry denied Keychain access | Stop, resolve normal exact-application access, retry once |
| Treat ad-hoc signing as Developer ID signing | Report the signing limitation explicitly |
