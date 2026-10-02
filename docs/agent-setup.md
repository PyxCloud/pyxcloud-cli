# Agent setup

Install the shared `passo` workflow skill for Codex or other agents:

```sh
passo setup codex
passo setup agents
```

Both commands manage the same `~/.agents/skills/passo/SKILL.md` file. Preview installation or removal without creating directories:

```sh
passo setup codex --dry-run
passo setup agents --dry-run --remove
```

The installer creates a private managed directory and ownership marker. It refreshes the skill only when the marker matches this CLI's owner ID, and refuses unmanaged directories, symlinked paths, and removal when unknown files are present. Remove the managed skill with `passo setup codex --remove`; removal deletes only `SKILL.md`, the ownership marker, and the now-empty `passo` directory. The installed workflow is in the `passo` skill and uses the CLI's current help and command catalog.

Run a local, read-only configuration check with:

```sh
passo doctor
passo doctor --json
```

The versioned JSON report checks the resolved profile endpoints, local ledger readability and scope, CLI executable availability, and whether the shared skill marker is `installed`, `unmanaged`, or `absent`. Missing ledger or skill setup is reported as a warning. Invalid profiles and invalid or mismatched ledgers exit with code 20. The command does not retrieve credentials, contact the API, or write files.
