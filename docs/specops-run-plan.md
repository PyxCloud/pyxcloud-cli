# Specops run plan format

`passo run --to <stage> --plan .passo/walk.json` executes a validated sequence of typed API operations through the selected stage. Projects operations belong to `connect`; define analysis belongs to `discover`, documentation belongs to `docs`, and scope assessment belongs to `define`. The accepted stages, in order, are `connect`, `discover`, `docs`, `define`, `freeze`, `secure`, `design`, `compare`, `seal`, `deploy`, and `monitor`. A plan uses schema version 1:

```json
{
  "schemaVersion": 1,
  "steps": [
    {
      "stage": "connect",
      "operation": "projects:projectRead",
      "params": {"projectId": "${projectId}"},
      "query": {},
      "bodyIdempotency": false
    },
    {
      "stage": "discover",
      "operation": "define:defineAnalysisStart",
      "params": {"projectId": "${projectId}"},
      "query": {},
      "input": {"source": "owner supplied data"},
      "bodyIdempotency": true,
      "check": {
        "operation": "define:defineAnalysisRead",
        "params": {"projectId": "${projectId}"},
        "query": {},
        "pointer": "/data/status",
        "equals": "ready"
      }
    }
  ]
}
```

Plan files are limited to 1 MiB and reject unknown fields, malformed JSON, unknown operations, operations in the wrong stage, and steps whose stages move backward. All steps are validated before any request, including steps after the requested target. Mutations need a GET check. Before a mutation, the GET predicate is read from the backend; a true predicate skips the mutation. Otherwise, the mutation is performed and the same GET is polled until its JSON Pointer value equals `equals` or the command times out. Ledger entries do not count as completion. GET steps execute once.

The supported parameter substitutions are `${projectId}`, `${versionId}`, `${versionSequence}`, `${releaseId}`, and `${runId}` from the runtime's canonical scope. `${versionId}` remains the frozen version UUID used by release routes. Cloud and security routes use `${versionSequence}`, the positive numeric sequence returned by freeze; it is empty until that authoritative value is available, and those route scope checks fail closed. Scope checks remain enforced by the runtime. Request bodies, queries, and operation names are authored explicitly; the driver does not invent decisions. Human actions, including deploy authorization and security remediation confirmation, return a human handoff with the project console path and never POST the action.
