# Canonical documentation commands

The `passo docs` commands expose the canonical documentation workspace, revision, and version-bound snapshot routes.

```sh
passo --project 42 docs workspace
passo --project 42 docs revisions --limit 20 --offset 0
passo --project 42 docs publish --input generation.json [--if-match 7]
passo --project 42 docs begin-review <revision-id> --if-match 7
passo --project 42 docs accept <revision-id> --if-match 8
passo --project 42 docs reject <revision-id> --if-match 8
passo --project 42 --version <version-uuid> docs snapshot read
passo --project 42 --version <version-uuid> docs snapshot lock --if-match 9
```

`publish` requires a JSON object with `runId` and `documentationId`; `If-Match` is optional there. Review decisions and snapshot lock require `--if-match`. The value is sent as a raw decimal header, including `0` when explicitly supplied. Those commands have no request body or `--input` flag. Mutations execute only the named operation and do not perform other documentation actions automatically.

Snapshot commands use the selected UUID `--version`. The CLI rejects a `projectVersionId` that differs from its selected runtime version; numeric cloud/security version sequences remain separate.
