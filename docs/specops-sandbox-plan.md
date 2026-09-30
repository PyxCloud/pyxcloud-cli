# SpecOps sandbox implementation plan

Goal: a thin `passo` CLI over public SpecOps APIs, with a deterministic local feedback loop and a separately evidenced real-provider acceptance walk.

## Constraints
- Preserve the legacy `pyxcloud` entry point and commands.
- Backend is authoritative; no SQL or in-process backend imports in the CLI.
- Human-only actions use browser handoff; CLI credentials never authorize them.
- Scope Lock uses `passo/release/<label>`; fixes advance the pin on the same version before seal.
- One shared sandbox, exclusively owned through the existing lock.
- No external model spending without the explicit product-run budget.
- Never mark fake-provider evidence as real-cloud acceptance.
- Luna workers receive explicit interfaces, file allowlists, checks, and STOP conditions.

## Deliverables
1. Contract and route inventory, worker procedure, baseline checks.
2. Profiles, secure token storage, PKCE/device login, transport, generated contracts, output and ledger.
3. Public-API stage commands for connect/discover/docs/define.
4. Scope Lock, scan/gate, architecture and cloud commands and necessary backend wiring.
5. Seal handoff and deploy lifecycle, deterministic runtime adapters, cancellation and cleanup.
6. Journey-based orchestration, monitor, evidence, deterministic agent and browser suites.
7. Real-provider acceptance, infrastructure automation, verified cleanup and cost.

## Acceptance
- `go test ./...` and `go build ./...` pass in the CLI worktree.
- No-op retries and resume are evidenced against the authoritative backend.
- Agent suite returns 10 at human gates; browser suite completes with test identity.
- Real walk: `/healthz` 200, duplicate run no-op, sweep leaves no resources, cost below $0.20.
- Real-provider acceptance remains NOT RUN until its actual evidence exists.
