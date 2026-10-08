# Scoped board work with Passo

These commands call the existing authenticated, project-scoped Board REST
surface. They never use MCP and never accept arbitrary execution commands,
repositories, model settings or credentials. Request schemas and illustrative
examples appear in command help and `passo commands --json`.

```sh
passo --profile staging --project "$PROJECT_ID" board status
passo --profile staging --project "$PROJECT_ID" board list
passo --profile staging --project "$PROJECT_ID" board task "$TASK_ID"
passo --profile staging --project "$PROJECT_ID" board availability "$TASK_ID"
passo --profile staging --project "$PROJECT_ID" board claim "$TASK_ID" --input claim.json
passo --profile staging --project "$PROJECT_ID" board plan "$TASK_ID" --input plan.json
passo --profile staging --project "$PROJECT_ID" board execute "$TASK_ID" --input execution.json
passo --profile staging --project "$PROJECT_ID" --timeout 2m --poll-interval 1s board execution "$EXECUTION_ID" --task "$TASK_ID" --wait
passo --profile staging --project "$PROJECT_ID" board latest "$TASK_ID"
passo --profile staging --project "$PROJECT_ID" board verify "$TASK_ID" --input actual-independent-review.json
passo --profile staging --project "$PROJECT_ID" board complete "$TASK_ID" --input actual-completion-evidence.json
passo --profile staging --project "$PROJECT_ID" board release "$TASK_ID" --input release.json
passo --profile staging --project "$PROJECT_ID" board resume "$TASK_ID" --input resume.json
passo --profile staging --project "$PROJECT_ID" board evidence "$ARTIFACT_ID"
```

Set identities from actual backend reads. Execution input is exactly
`{"commandId":"<caller-owned UUID>"}`; the backend owns all executable inputs.
An accepted execution is not task completion. Poll the returned durable
execution UUID, requiring its exact task identity. Wait is bounded by timeout;
failed/cancelled/unknown or foreign receipts cannot report success.

Claim defaults to the caller's console identity; only the backend can authorize
another owned identity. Do not fabricate lease fences, usage or review. Verify
records an actual independent verdict and preserves server self-verification
refusal. Complete invokes the canonical gate chain. HTTP200 `status:blocked`
emits blocked evidence, records a blocked ledger entry and exits20. Malformed
or contradictory DONE responses are uncertain and exit30. A real DONE requires
its complete response with empty missing/failing checks; no movement,
reconciliation or local ledger can replace that proof.

No client-only budget cap is introduced. Authorized paid work must still be
minimized and its actual measured cost reported when available; unknown costs
remain unknown.

The hand-maintained Board REST registry is sourced from backend mounted
handlers and `docs/console/BOARD-REST-CONTRACT.md`. It is separate from existing
generated OpenAPI snapshots because that backend surface is not yet published
as OpenAPI. No contract snapshot is rewritten by these commands.
