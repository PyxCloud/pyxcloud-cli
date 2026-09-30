# Local SpecOps feedback entrypoint

`scripts/specops-feedback.py` is the versioned entrypoint for local M0/M1 work. It has two modes:

- `check` is read-only. It checks the explicit CLI/backend/database paths, pinned fixture files, the running `pyx-sandbox` database container (`db`, host port `15432`), the owned native launcher metadata, and backend `/readyz` (`200`). It does not start, stop, or write to services.
- `walk --to m0` runs the existing M0 smoke. `walk --to m1` runs M0 followed by the durable M1 fixture walk. The walk reuses the already-running owned stack and versioned native backend. It does not boot a second stack. These public CLI workflows may create the named local fixture projects and upload the acceptance document if the existing scripts determine that is required.

M2, M3, and M4 are deliberately unsupported. For example, `walk --to m2` exits with status `20` before preflight or workflow calls. A successful `/readyz` check is only a prerequisite check; it does not certify any workflow phase.

## Reuse the running sandbox

Build the CLI binary once and provide all four paths explicitly. The Python dependency is needed by the existing PKCE M0/M1 scripts; use a local virtual environment so the machine Python remains unchanged.

```sh
CLI_REPO="$(git rev-parse --show-toplevel)"
BACKEND_REPO="/absolute/path/to/pyx-backend-checkout"
DATABASE_REPO="/absolute/path/to/database-checkout"
RUNTIME_DIR="/absolute/path/to/private/specops-runtime"
CLI_BIN="/absolute/path/to/specops-passo"

python3 -m venv /tmp/specops-feedback-venv
/tmp/specops-feedback-venv/bin/python -m pip install requests
(cd "$CLI_REPO" && go mod download && go build -o "$CLI_BIN" .)
```

`RUNTIME_DIR` must be the private directory already used by the versioned backend launcher. Do not substitute another runtime path: the metadata and owned-process check bind to that exact directory.

Run the read-only check:

```sh
/tmp/specops-feedback-venv/bin/python "$CLI_REPO/scripts/specops-feedback.py" check \
  --backend-repo "$BACKEND_REPO" \
  --database-repo "$DATABASE_REPO" \
  --runtime-dir "$RUNTIME_DIR" \
  --cli "$CLI_BIN"
```

Run M0 only or the M0→M1 path, with an overall walk cap from 60 to 1800 seconds:

```sh
/tmp/specops-feedback-venv/bin/python "$CLI_REPO/scripts/specops-feedback.py" walk --to m0 \
  --backend-repo "$BACKEND_REPO" \
  --database-repo "$DATABASE_REPO" \
  --runtime-dir "$RUNTIME_DIR" \
  --cli "$CLI_BIN" \
  --max-runtime 900

/tmp/specops-feedback-venv/bin/python "$CLI_REPO/scripts/specops-feedback.py" walk --to m1 \
  --backend-repo "$BACKEND_REPO" \
  --database-repo "$DATABASE_REPO" \
  --runtime-dir "$RUNTIME_DIR" \
  --cli "$CLI_BIN" \
  --max-runtime 1200
```

The wrapper supplies explicit realm, repository-source, acceptance-fixture, evidence, and stable project-name arguments to the existing scripts. M0 uses `specops-local-feedback`; M1 uses `specops-local-fixture-walk`. M1’s acceptance upload is labelled as a user acceptance fixture, not repository proof. The source fixture is accepted only when its manifest identity/commit and pinned file checksum match.

Each walk writes a compact schema-version-1 record below `$RUNTIME_DIR/feedback-evidence/` with phase status, local Git revisions, timings, and validated project/run/compilation identifiers. Directories use mode `0700`; the final record uses `0600`. It records no tokens, command output, response bodies, or authentication payloads. Failures expose only a typed message, and timed-out subprocess process groups are terminated.

## Cold boot, performed separately

The feedback entrypoint never performs cold boot. If the sandbox is stopped, use the normal backend `sandbox.sh` workflow manually, with explicit database migration paths. The script obtains any required local build credentials using its existing local credential flow; do not paste credentials into commands or evidence.

```sh
DATABASE_REPO_APPLY_SCRIPT="$DATABASE_REPO/scripts/apply-migrations.sh" \
DATABASE_REPO_MIGRATIONS="$DATABASE_REPO/sql/migrations" \
  "$BACKEND_REPO/scripts/sandbox.sh" up db sso
```

After the migration service completes successfully, the dedicated discovery database roles can be provisioned through the compose one-shot service. This command is for the owner of the active `pyx-sandbox` stack; it deliberately skips dependencies so it cannot rerun the migrator:

```sh
DATABASE_REPO_APPLY_SCRIPT="$DATABASE_REPO/scripts/apply-migrations.sh" \
DATABASE_REPO_MIGRATIONS="$DATABASE_REPO/sql/migrations" \
  docker compose -p pyx-sandbox -f "$BACKEND_REPO/sandbox/docker-compose.yml" \
  --profile backend run --no-deps --rm discovery-roles
```

Build the local offline backend and start it through the versioned launcher. Go module access must already be configured through the operator’s normal local credential helper; credentials are not added to the launcher environment or evidence.

```sh
install -d -m 700 "$RUNTIME_DIR"
(cd "$BACKEND_REPO/go" && go mod download && \
  go build -o "$RUNTIME_DIR/specops-backend-offline-local" ./cmd/server)
python3 "$BACKEND_REPO/sandbox/specops/start-backend.py" start \
  --binary "$RUNTIME_DIR/specops-backend-offline-local" \
  --runtime-dir "$RUNTIME_DIR" \
  --state-dir "$RUNTIME_DIR/state"
python3 "$BACKEND_REPO/sandbox/specops/start-backend.py" status --runtime-dir "$RUNTIME_DIR"
```

Once the owned status command reports `running` and `/readyz` returns `200`, the read-only `check` can verify the full reuse preconditions. Stop only the process proved to be owned by that same runtime metadata:

```sh
python3 "$BACKEND_REPO/sandbox/specops/start-backend.py" stop --runtime-dir "$RUNTIME_DIR"
```

Do not use `docker compose down`, remove volumes, or switch runtime directories as part of a feedback walk. Shared stack lifecycle remains an explicit operator action.

## Verification commands

From the CLI checkout, run the mocked wrapper tests and the existing M0/M1 harness tests:

```sh
python3 -m unittest scripts/test_specops_feedback.py
python3 -m unittest scripts/test_specops_smoke.py scripts/test_specops_m1.py
```

The wrapper unit tests do not call Docker, the backend, Keycloak, or the public workflow APIs.

## Red Flags — STOP

- If repository roots, fixture provenance, Docker labels/port, launcher ownership, or readiness differ, stop before starting a walk.
- If `/readyz` succeeds but launcher status does not prove the exact owned process, do not reuse the port.
- If a walk times out or reports an uncertain fixture upload, inspect compact evidence and public state before any retry.
- If the requested phase is M2, M3, or M4, stop; those phases have not been implemented by this entrypoint.

| Common mistake | Required action |
|---|---|
| Treating `check` as workflow acceptance | Read it only as local prerequisite evidence; phase acceptance comes from the walk’s validated terminal state. |
| Starting a second backend because readiness is slow | Stop and inspect the owned launcher status and logs; never guess which PID owns the port. |
| Running M2/M3/M4 through a guessed sequence | Stop; the wrapper rejects those phases with exit status `20`. |
| Saving raw CLI or API output to diagnose a failure | Preserve the compact typed evidence; inspect local logs through the owning process separately. |
