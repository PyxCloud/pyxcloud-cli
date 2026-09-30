# Local SpecOps feedback loop

`scripts/specops-feedback.py` provides a read-only preflight and a bounded public-CLI walk through M0 or M0→M1. It requires explicit CLI, backend, database, and private runtime paths. M2–M4 fail with exit status `20` before inspection or mutation.

The preflight verifies a local Unix-socket Docker context; the `pyx-sandbox` database on exactly `127.0.0.1:15432`; the database and Keycloak on the approved internal network; the SSO forwarder on exactly `127.0.0.1:18081`; a deterministic private Keycloak realm export and persistent named H2 volume; and the owned native backend’s ready state and self-tested macOS sandbox profile hash. It never starts or stops services. A network boundary does not make the backend database login least-privileged: do not run the native backend with the `pyx` superuser/BYPASSRLS role. A separately provisioned non-superuser runtime role is a prerequisite for an accepted safe walk.

Before execution, the runner requires clean tracked copies of its four versioned inputs (`specops-feedback.py`, `specops-smoke.py`, `specops-m1.py`, and `specops-acceptance.md`) and records their exact paths and SHA-256 hashes, the CLI binary hash, and repository revisions. It checks these again before and after each workflow. The M0 and M1 Python harnesses and the CLI run inside the backend launcher’s self-tested `feedback-client` macOS sandbox role. Its network policy permits only loopback API/SSO endpoints; a resolver shim maps only `sso.localtest.me:18081` to `127.0.0.1`, preserving the URL host and issuer. There is no unconfined fallback.

M0 checks two matching public status reads with no mutation. M1 bootstraps a fixture project through the public API, runs discovery, creates docs only when absent or stale, compiles the checked-in acceptance fixture twice with identical inputs, applies the nonempty scope, and verifies canonical state and journey reads. The uploaded acceptance markdown is explicitly a user acceptance fixture; it is not repository proof. Repository proof is restricted to the checked-in source fixture whose manifest identity, commit, and content hash match the pinned facts.

Each walk records compact private evidence under `$RUNTIME_DIR/feedback-evidence` using mode `0700` for directories and `0600` for records. It contains phase outcomes, validated identifiers, source hashes, revisions, and timings; it contains no tokens, raw API/CLI bodies, or authentication payloads. Child exit success alone is insufficient: the wrapper validates canonical workflow evidence and expected public states.

## Build and reuse an already-running safe sandbox

Use a local Python environment for the existing PKCE helpers and build the CLI from the checkout you intend to certify. The API token, model keys, Docker environment, and shell credentials are not forwarded to guarded workflow children.

```sh
CLI_REPO="$(git rev-parse --show-toplevel)"
BACKEND_REPO="/absolute/path/to/pyx-backend-checkout"
DATABASE_REPO="/absolute/path/to/database-checkout"
RUNTIME_DIR="/absolute/path/to/private/specops-runtime"
CLI_BIN="/absolute/path/to/specops-passo"
PYTHON="/absolute/path/to/feedback-venv/bin/python"

"$PYTHON" -m pip install requests
(cd "$CLI_REPO" && go mod download && go build -o "$CLI_BIN" .)
```

The runtime directory must be the private directory owned by the versioned native backend launcher. Run the read-only check:

```sh
"$PYTHON" "$CLI_REPO/scripts/specops-feedback.py" check \
  --backend-repo "$BACKEND_REPO" --database-repo "$DATABASE_REPO" \
  --runtime-dir "$RUNTIME_DIR" --cli "$CLI_BIN"
```

Run M0 or the bounded M0→M1 walk (60–1800 seconds):

```sh
"$PYTHON" "$CLI_REPO/scripts/specops-feedback.py" walk --to m0 \
  --backend-repo "$BACKEND_REPO" --database-repo "$DATABASE_REPO" \
  --runtime-dir "$RUNTIME_DIR" --cli "$CLI_BIN" --max-runtime 900

"$PYTHON" "$CLI_REPO/scripts/specops-feedback.py" walk --to m1 \
  --backend-repo "$BACKEND_REPO" --database-repo "$DATABASE_REPO" \
  --runtime-dir "$RUNTIME_DIR" --cli "$CLI_BIN" --max-runtime 1200
```

The walk explicitly provisions and self-tests the feedback-client guard role before invoking the children. To provision it separately after backend startup, use the exact versioned launcher command (this changes only private runtime policy metadata; `check` remains read-only):

```sh
"$PYTHON" "$BACKEND_REPO/sandbox/specops/start-backend.py" guarded-exec \
  --role feedback --runtime-dir "$RUNTIME_DIR" --read-dir "$CLI_REPO" \
  --read-dir "$($PYTHON -c 'import sys; print(sys.prefix)')" \
  --executable "$(realpath "$PYTHON")" --read-file "$(realpath "$CLI_BIN")" --validate-only
```

## Safe cold boot

Cold boot is separate from feedback `check` and `walk`. Use only the versioned `specops-safe-up` command, which validates the checked-in safe Compose overlay, pins cached images without pulling, uses the existing stack lock, imports stable fixture user IDs, binds host ports to loopback, and creates the internal network and persistent Keycloak data volume. It requires the normal database migration script and migration directory from the same explicit database checkout. It does not read the project `.env`, request GitHub credentials, build images, or start the application backend.

```sh
export SPECOPS_COMPOSE_SAFE_OVERLAY="$BACKEND_REPO/sandbox/specops/compose.safe.yml"
export SPECOPS_RUNTIME_DIR="$RUNTIME_DIR"
export DATABASE_REPO_APPLY_SCRIPT="$DATABASE_REPO/scripts/apply-migrations.sh"
export DATABASE_REPO_MIGRATIONS="$DATABASE_REPO/sql/migrations"
install -d -m 700 "$RUNTIME_DIR"
"$BACKEND_REPO/scripts/sandbox.sh" specops-safe-up
```

The `migrate` and `discovery-roles` one-shot services run in that locked safe startup. Do not run a second compose migration command. The backend binary and API lifecycle remain owned by `sandbox/specops/start-backend.py`; build the offline binary using the project’s normal local Go dependency setup, then start and verify it:

```sh
install -d -m 700 "$RUNTIME_DIR"
(cd "$BACKEND_REPO/go" && go mod download && \
  go build -o "$RUNTIME_DIR/specops-backend-offline-local" ./cmd/server)
"$PYTHON" "$BACKEND_REPO/sandbox/specops/start-backend.py" start \
  --binary "$RUNTIME_DIR/specops-backend-offline-local" \
  --runtime-dir "$RUNTIME_DIR" --state-dir "$RUNTIME_DIR/state"
"$PYTHON" "$BACKEND_REPO/sandbox/specops/start-backend.py" status --runtime-dir "$RUNTIME_DIR"
```

Use the approved non-superuser runtime database login before starting the backend. The `pyx` migration/admin login is not an acceptable API runtime identity. Keep the safe stack lock and named volumes intact; do not use ordinary `sandbox.sh down`, `docker compose down`, or volume deletion for feedback cleanup. Lifecycle stop must use the separately reviewed safe-stop operation once available.

## Verification

From the CLI checkout, run:

```sh
python3 -m unittest scripts.test_specops_feedback scripts.test_specops_smoke scripts.test_specops_m1
```

These tests mock Docker and workflow subprocesses; they do not contact or mutate the shared sandbox.

## Red Flags — STOP

- Refuse the walk if any tracked harness source is dirty, a source hash changes, the CLI binary changes, or any explicit checkout does not match the executed paths.
- Refuse Docker metadata unless it proves a local Unix socket, exact project/service labels, loopback-only host binds, the named H2 volume, and the internal network.
- Refuse the native backend if launcher ownership, ready state, guard self-test, policy hash, or feedback-client role hash is missing or inconsistent.
- Stop after timeouts or uncertain uploads; inspect private compact evidence and public state before deciding on a retry.
- Stop for M2–M4; the wrapper has no supported workflow for those phases.

| Common mistake | Required action |
|---|---|
| Treating `check` as workflow acceptance | Use it only as prerequisite evidence; phase acceptance requires validated public state from the walk. |
| Editing the checked-in harness before a certified walk | Commit or revert the tracked edits, rebuild from that checkout, then rerun preflight. |
| Starting services with ordinary `sandbox.sh up` or running `down` | Stop; use the explicit safe-up command and preserve the owned lock and volumes. |
| Starting a second backend because readiness is slow | Check the same runtime’s launcher status; do not guess which process owns the API port. |
| Saving raw API or CLI output | Keep compact private evidence only; do not persist tokens or response bodies. |
