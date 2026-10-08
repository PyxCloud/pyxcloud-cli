# Actual baseline scan from a local agent

`passo secure scan-baseline` sends a bodyless POST to `/vibe/projects/{projectId}/security/scan-baseline` through the normal authenticated transport and stable idempotency ledger. The backend chooses the current locked version and frozen source pins; the CLI sends no repository, branch, scan result, or approval assertion.

```sh
passo --profile staging --project 64 --version-sequence 12 --json --timeout 10m --poll-interval 2s secure scan-baseline --wait
passo --profile staging --project 64 --version-sequence 12 --json secure gate
```

Use actual verified scope values, not these examples for another project/version. `--wait` requires the returned run ID, project and version to match each canonical GET scan read. Only COMPLETED produces `status=completed`; FAILED, an unknown state, a different run, or timeout refuses. Without `--wait`, the output is only the accepted receipt. The deadline bounds trigger plus polling. The normal backend one-live-run and scan-rate guards remain authoritative.

For an actual recording, give the local agent the English request: “Review Pharos for release. Run the security checks, explain what needs fixing, and prepare a prioritized work plan. Do not deploy.” Record the real request, tool calls and canonical result. The agent may write a source-backed work plan after the scan. A scan result is not proof that remediation was executed.

Authentication must be the legitimate human OAuth session. A memory-only `PASSO_ACCESS_TOKEN` child environment is supported; never put a token in arguments, transcripts or output. Do not run the existing keychain save path where session policy prohibits secret arguments. Stop on rejected auth, uncertain mutation, mismatched scope or missing current lock; re-read canonical state before retrying. Human remediation confirmation and deployment remain explicit handoffs.
