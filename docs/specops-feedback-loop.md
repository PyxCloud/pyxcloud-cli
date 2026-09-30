# M0 local feedback loop

This harness exercises the integrated `passo` CLI against the real local API. It performs a Keycloak browser-form login with PKCE S256 using credentials read in memory from the imported realm fixture, checks OAuth callback origin/path/state, exchanges the authorization code, reads the public project list, then invokes CLI `status` twice. The token is passed only as `PASSO_ACCESS_TOKEN` in the child process environment. No model call, Docker command, token file, or raw API/identity payload is used.

The evidence file contains only the local provider/auth mode, project identity, timestamps, and the validated status schema/profile/stage/next-action key. The harness fails closed unless API and issuer are plain HTTP local endpoints (with the explicit `sso.localtest.me` issuer exception). It reuses the named project when present. If it is absent, the integrated CLI must support `projects create --input ... --json`; creation is never performed through a REST fallback.

## Measure → act → verify

1. Confirm local prerequisites without starting containers:

   ```sh
   curl --fail --silent http://127.0.0.1:16080/readyz
   test -f ../specops-backend/sandbox/realm-export.json
   test -x /tmp/specops-passo
   ```

2. Run the deterministic harness unit checks:

   ```sh
   python3 -m unittest scripts/test_specops_smoke.py
   ```

3. Exercise the real local feedback loop:

   ```sh
   python3 scripts/specops-smoke.py \
     --cli /tmp/specops-passo \
     --backend http://127.0.0.1:16080 \
     --issuer http://sso.localtest.me:18081/realms/passobuild \
     --realm-fixture ../specops-backend/sandbox/realm-export.json \
     --project-name specops-local-feedback \
     --evidence-dir /tmp/specops-m0-evidence
   ```

4. Verify the compact evidence and confirm it contains no token or response body:

   ```sh
   python3 -c 'import json; p="/tmp/specops-m0-evidence/m0-smoke.json"; d=json.load(open(p)); assert d["providerMode"]=="local" and d["authMode"]=="browser-pkce" and d["readCount"]==2 and d["secondReadNoMutation"]; assert d["first"]==d["second"]; assert "access_token" not in open(p).read() and "refresh_token" not in open(p).read(); print("M0 evidence verified:", d["projectId"], d["first"]["stage"], d["first"]["nextActionKey"])'
   ```

This is the M0 local feedback-loop smoke. It is not a claim of full end-to-end product coverage.

## M1 durable SpecOps fixture walk

`scripts/specops-m1.py` runs the local discovery, documentation, compilation and scope path through the integrated `passo` CLI. Its stable default project is `specops-local-fixture-walk`, deliberately separate from the earlier M0 smoke project. It reuses the M0 PKCE and project-list helpers. The only REST write is the harness bootstrap upload for `fixtures/specops-acceptance.md`; it is labelled as a user acceptance fixture, never as repository proof. The source-fixture SHA is recorded as a checksum of that local fixture file, not as discovered repository provenance.

Every CLI response is schema/profile/project validated where the command emits a command envelope. The discovery run must succeed with a source snapshot containing a repository commit. Documentation is generated only when absent, stale, or from a different run. Compilation names the fixture document ID and deterministic `idempotencyKey`; a second compile must return the same compilation ID and revision. Scope derivation and apply use the CLI; a CLI run plan then checks the canonical project state contains the derived candidate ID, followed by a project journey read. Polls, subprocesses, HTTP requests, and total runtime have explicit deadlines. CLI stdout/stderr and API response bodies are never copied to evidence or printed on error.

### Measure → act → verify

1. Check local prerequisites without starting or mutating containers:

   ```sh
   curl --fail --silent http://127.0.0.1:16080/readyz
   test -f .worktrees/specops-backend/sandbox/realm-export.json
   test -f .worktrees/specops-backend/sandbox/specops/fixture/files/tinyGoApp.go
   test -x /tmp/specops-passo
   ```

2. Run deterministic harness validation:

   ```sh
   python3 -m unittest scripts/test_specops_m1.py
   python3 -m unittest scripts/test_specops_smoke.py
   ```

3. Run the real local walk only when the owner has explicitly scheduled the shared sandbox:

   ```sh
   python3 scripts/specops-m1.py \
     --cli /tmp/specops-passo \
     --backend http://127.0.0.1:16080 \
     --issuer http://sso.localtest.me:18081/realms/passobuild \
     --acceptance-fixture fixtures/specops-acceptance.md \
     --project-name specops-local-fixture-walk \
     --evidence-dir /tmp/specops-m1-evidence
  ```

By default, the realm and health source fixtures resolve from the workspace's local `.worktrees/specops-backend/sandbox` checkout; pass explicit `--realm-fixture` and `--source-fixture` paths when that checkout lives elsewhere.

4. Verify compact evidence only:

   ```sh
   python3 -c 'import json, pathlib; p=pathlib.Path("/tmp/specops-m1-evidence/m1-specops.json"); d=json.loads(p.read_text()); o=d["outcomes"]; assert d["workflow"]=="M1-local-specops-fixture-walk" and o["discoveryStatus"]=="succeeded" and o["repeatCompileSameIdentity"] and o["canonicalScopeStateVerified"]; assert p.stat().st_mode & 0o777 == 0o600; assert "access_token" not in p.read_text() and "refresh_token" not in p.read_text(); print("M1 evidence verified:", d["projectId"], o["discoveryRunId"], o["compilationRevision"])'
   ```

The harness fails closed on malformed CLI envelopes, non-success or unproven discovery, document ambiguity or size mismatch, stale documentation, polling exhaustion, changed repeat-compilation identity, or missing persisted scope. Existing documents are reusable only when exactly one READY row matches the hash-bearing filename and byte size. An upload with uncertain outcome is not retried automatically. Evidence mode is `0600`, its directory is `0700`, and the record contains compact IDs, local fixture SHA, timings, and outcomes only.

### Red Flags — STOP

- If readiness, fixture files, or CLI binary are missing, stop without starting services.
- If the document filename is duplicated, its size differs, its status is not READY, or upload verification is uncertain, stop; do not upload a second copy.
- If discovery lacks commit provenance or does not reach `succeeded` before the poll cap, stop; do not generate documentation.
- If a CLI command fails or emits an invalid/wrong-project envelope, stop; do not fall back to direct REST for workflow operations.
- If the repeat compile changes ID/revision or canonical state lacks the derived candidate, stop before claiming success.

| Common mistake | Required action |
|---|---|
| Treating the user acceptance fixture as repository evidence | Keep its `user_acceptance_fixture_not_repository_proof` label in evidence. |
| Re-running a possibly timed-out fixture upload | Stop and inspect the unique document listing before any later invocation. |
| Logging command output to diagnose a failure | Use the typed failure class and local CLI/server logs; never persist raw output or bodies. |
| Starting another project because the default exists | Reuse the stable project name and fail on duplicate matches. |
