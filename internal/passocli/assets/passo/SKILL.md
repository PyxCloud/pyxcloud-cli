---
name: passo
description: Use the passo CLI to inspect and advance SpecOps workflows with backend evidence, bounded waits, and explicit human handoffs.
---

# Passo workflow

Use the public CLI as the workflow interface. The local ledger helps resume work; only current backend reads prove state. Do not print, copy into prompts, or include `PASSO_ACCESS_TOKEN` in reports. The CLI can use its keychain credential without exposing the token.

## 1. Measure

Set the project ID from the user's request, then inspect the actual command surface and current journey:

```sh
export PASSO_PROJECT_ID=123
passo --help
passo commands --json
passo --json --project "$PASSO_PROJECT_ID" status
passo --json --project "$PASSO_PROJECT_ID" projects get
```

Use `passo <command> --help` before choosing an action or input shape. For JSON input, create a file with the exact documented request body and pass its path with `--input`; never guess fields. For example, `passo --json --project "$PASSO_PROJECT_ID" define apply` derives and applies the compiled scope contract.

## 2. Act

Run only the stage the user authorized. Use the bounded workflow runner when a validated plan is available:

```sh
passo --json --project "$PASSO_PROJECT_ID" --timeout 2m --poll-interval 1s run --to discover --plan "$PASSO_PLAN"
```

Set `PASSO_PLAN` to the user's approved, existing plan file before running that command. Set `PASSO_DOC_INPUT` and `PASSO_FREEZE_INPUT` to exact JSON request files before using the corresponding commands below.

Direct stage commands use the same project scope, for example `passo --json --project "$PASSO_PROJECT_ID" discover start` or `passo --json --project "$PASSO_PROJECT_ID" docs generate --input "$PASSO_DOC_INPUT"`. Freeze input is a JSON file: `passo --json --project "$PASSO_PROJECT_ID" freeze create --input "$PASSO_FREEZE_INPUT"`.

Cloud and security routes use the positive numeric sequence returned by freeze (`--version-sequence`); release routes use the version UUID (`--version`). Release branch preview/materialization uses the separate Git-safe version label (`--version-label`), returned as `versionLabel` by freeze or version-lock creation and persisted in the ledger. Never convert one identity into another or guess a mapping. Missing identity is a stop condition. `freeze branches create` derives its request body from `--version-label`; it does not take an input file. `--timeout` and `--poll-interval` bound waiting; an accepted HTTP response is not proof of completion, and a ledger entry is not proof either.

Use the canonical values explicitly when reading those APIs:

```sh
passo --json --project "$PASSO_PROJECT_ID" --version-sequence "$PASSO_VERSION_SEQUENCE" compare
passo --json --project "$PASSO_PROJECT_ID" --version-sequence "$PASSO_VERSION_SEQUENCE" secure gate
passo --json --project "$PASSO_PROJECT_ID" --version-label "$PASSO_VERSION_LABEL" freeze branches
passo --json --project "$PASSO_PROJECT_ID" --version-label "$PASSO_VERSION_LABEL" freeze branches create
```

## 3. Verify

Read canonical backend state after each mutation, using `status` and the relevant stage read command. For example:

```sh
passo --json --project "$PASSO_PROJECT_ID" status
passo --json --project "$PASSO_PROJECT_ID" define assessment-read
```

Check the command exit code and JSON `status`/`code`: `0` means completed, `10` means a human action is required, `20` means invalid input or failed scope, and `30` means backend state is unavailable or uncertain. Retry only after a fresh backend read resolves uncertainty.

## Stop conditions

- If the backend read disagrees with the requested project or stage, stop and report the observed scope.
- If a command exits `10`, send the user to the returned browser/console action; never authorize deployment or confirm remediation from the CLI.
- If a mutation is uncertain or the backend is unavailable, stop before retrying and preserve the reported status.
- If the profile uses a sandbox fixture, describe results as fixture evidence; do not claim a real cloud deployment or external repository action.
- Do not invoke paid model work unless the user gives an explicit budget.

## Common mistakes

| Mistake | Correct action |
| --- | --- |
| Treating ledger state or HTTP `202` as completion | Re-read the canonical backend state. |
| Passing a version UUID to a cloud/security route | Use the authoritative numeric `--version-sequence`. |
| Passing a version UUID to release branch preview | Use the authoritative `--version-label` returned by freeze or version-lock creation. |
| Treating exit `10` as failure to automate | Preserve the human browser handoff. |
| Reporting sandbox fixture output as a real deployment | Label it as fixture evidence only. |
