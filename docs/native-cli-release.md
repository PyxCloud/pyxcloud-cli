# Native macOS CLI release protocol

The macOS OAuth store uses Security.framework through cgo. A Linux-built
CGO_DISABLED Darwin executable cannot persist credentials and fails closed.
The standalone Passo binary must also be shipped and installed; it is not the
legacy `pyxcloud board` MCP command.

## Measure → act → verify

1. Review the source commit and run source checks:

```sh
python3 scripts/test_native_cli_release.py
goreleaser check
actionlint -shellcheck='' .github/workflows/releaser.yml
bash -n scripts/build-native-cli.sh
bash -n scripts/install.sh
```

2. On an actual matching macOS host, build an archive with both executables:

```sh
scripts/build-native-cli.sh arm64 /absolute/owned/artifact-directory
# On an actual Intel Mac, use amd64 instead.
```

The script refuses other OS/architecture combinations, compiles with cgo,
checks actual Security.framework linkage and binary signatures, and verifies
the Passo help command before creating the archive. It does not authenticate,
call a customer workflow, create a tag, or publish a release.

3. After review and merge, dispatch the existing release workflow on the exact
reviewed source with `publish=false` for artifact verification. The workflow
runs Intel (`macos-15-intel`) and Apple Silicon (`macos-15`) native jobs, including
an owned dummy Keychain fixture in a disposable CI Keychain. Linux/Windows stay
portable cgo-disabled builds. Publication depends on all three build jobs.

```sh
gh workflow run releaser.yml --repo PyxCloud/pyxcloud-cli --ref main -f publish=false
```

4. Only the release owner may create the reviewed version tag and dispatch with
`publish=true`. The workflow refuses a branch, verifies the existing tag points
to the checked-out source, aggregates all archives and checksums, creates a draft
release with `--verify-tag`, and makes it public after uploading complete assets.
Do not publish or create a tag merely to test this protocol.

## Fail-closed / STOP actions

- If host architecture, Keychain fixture, framework linkage, signature, source
  check or any matrix job fails, stop before publication.
- If a release already exists, stop; do not clobber assets.
- If the native macOS runner is unavailable, report that architecture unverified;
  never substitute a Linux cross-build.
- If Keychain identity or canonical API access differs from the owned human
  session, stop workflow mutations and preserve the actual refusal.

## Signing boundary

The current script uses ad-hoc signatures for binary integrity. This is not an
Apple Developer ID signature or notarization. No Developer ID credential or
notarization authority was found or introduced. Do not claim Gatekeeper release
approval; a separately approved signing issuer is required for that claim.

## Common mistakes

| Mistake | Correct action |
| --- | --- |
| Publish macOS with CGO_ENABLED=0 | Use matching native macOS runners and framework verification. |
| Ship only legacy pyxcloud | Archive and install both pyxcloud and standalone passo. |
| Publish Linux assets before native jobs finish | Publish only after portable and both native jobs pass. |
| Treat ad-hoc signing as notarization | State the exact signing boundary and preserve its separate prerequisite. |
| Use a static bearer read as durable OAuth proof | Verify normal human PKCE, stable subject, native storage and actual refresh. |

## Measured Linux runner availability

On 2026-10-08 the repository and organization self-hosted inventories reported zero registered runners. Build-only run 37758518071 remained queued on legacy pyxflow labels; run 37759892241 remained queued on ubicloud-standard-2 while both native jobs succeeded. This CLI repository's ubuntu-latest contract checks repeatedly completed successfully, so Linux packaging and gated publication use that same available hosted runner. Each job retains its 15-minute execution timeout. Runner execution time and billing must be read from the actual workflow/account usage; these observations do not establish a dollar cost or a guaranteed queue deadline. No release is published by build-only runs.
