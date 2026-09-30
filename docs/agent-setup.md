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
