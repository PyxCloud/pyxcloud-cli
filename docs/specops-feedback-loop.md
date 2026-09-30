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
