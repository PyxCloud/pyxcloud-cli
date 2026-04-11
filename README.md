<p align="center">
  <img src=".github/logo.png" alt="PyxCloud" width="80" />
</p>

<h1 align="center">PyxCloud CLI</h1>

<p align="center">
  The official command-line interface for the <a href="https://pyxcloud.io">PyxCloud Platform</a>.<br/>
  Design, compare, deploy, and manage multi-cloud infrastructure — from your terminal or CI/CD pipeline.
</p>

<p align="center">
  <img src="https://img.shields.io/badge/version-0.2.0-0076d1?style=flat-square" alt="Version 0.2.0" />
  <img src="https://img.shields.io/badge/license-proprietary-333?style=flat-square" alt="License" />
  <img src="https://img.shields.io/badge/go-1.25-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.25" />
  <img src="https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20windows-blue?style=flat-square" alt="Platforms" />
</p>


---

## Overview

Every command in the CLI maps 1:1 to the PyxCloud console sidebar — projects, architecture, key management, settings — so you can operate your entire cloud estate without a browser.

The CLI also ships a **Local Shell Bridge** (`pyxcloud proxy`) that lets the web console open real SSH sessions to your servers through a secure localhost WebSocket, bypassing browser security constraints entirely.

---

## Installation

### macOS & Linux — Universal Installer (recommended)

```bash
curl -sL https://github.com/PyxCloud/pyxcloud-cli/releases/latest/download/install.sh | bash
```

The script auto-detects your OS and architecture, installs the binary to `/usr/local/bin`, and configures shell autocompletion.

### macOS — Homebrew

```bash
brew tap pyxcloud/tap
brew install pyxcloud
```

### Windows — Scoop

```powershell
scoop bucket add pyxcloud https://github.com/PyxCloud/scoop-bucket.git
scoop install pyxcloud
```

### Manual Download

Download the archive for your platform from the [Releases page](https://github.com/PyxCloud/pyxcloud-cli/releases/latest), extract it, and move the binary into your `$PATH`.

| Platform       | Architecture     | Format   |
|----------------|------------------|----------|
| macOS          | x86_64, arm64    | `.tar.gz` |
| Linux          | x86_64, arm64, i386 | `.tar.gz`, `.deb`, `.rpm`, `.apk` |
| Windows        | x86_64, arm64, i386 | `.zip`   |

### Verify Checksums

Every release publishes a `checksums.txt` signed alongside the binaries:

```bash
sha256sum -c checksums.txt
```

---

## Authentication

### Interactive (SSO via browser)

```bash
pyxcloud auth login
```

Opens your default browser for PKCE-secured SSO authentication via Keycloak. An offline token is stored locally so sessions persist across reboots.

### Non-interactive (CI/CD pipelines)

```bash
# With an API token (created via `settings create-token`)
pyxcloud auth login --token pyxc_abc123...

# With a JWT access token
pyxcloud auth login --token eyJhbGciOi...

# With environment variable
export PYXCLOUD_TOKEN=pyxc_abc123...
```

### Custom Endpoints

```bash
pyxcloud auth login \
  --url https://api.pyxcloud.io \
  --auth-url https://auth.pyxcloud.io/realms/pyx \
  --client-id pyxcloud-cli
```

### Logout

```bash
pyxcloud auth logout
```

---

## Command Reference

### `pyxcloud projects`

Manage projects in the current organisation.

```bash
# List all projects (default action)
pyxcloud projects
pyxcloud projects list

# Create a new project
pyxcloud projects create --name "production-stack"
pyxcloud projects create --name "staging-env" --description "Staging environment"

# Delete a project and all its builds
pyxcloud projects delete --id 42
pyxcloud projects delete --id 42 --force
```

---

### `pyxcloud architecture` (alias: `arch`)

Full infrastructure lifecycle — build enumeration, cost comparison, deployment, status monitoring, and teardown.

#### `architecture builds`

```bash
pyxcloud arch builds -p 42
```

#### `architecture compare`

Retrieve the multi-cloud pricing comparison table — same data rendered in the console UI.

```bash
# Auto-detect architecture root nodes
pyxcloud arch compare -p 42 -v 0.1.0

# Filter to a specific resource table
pyxcloud arch compare -p 42 -v 0.1.0 --table load-balancer
```

#### `architecture deploy`

```bash
# Interactive (prompts for provider credentials)
pyxcloud arch deploy -p 42 -v 0.1.0

# Non-interactive with inline credentials
pyxcloud arch deploy -p 42 -v 0.1.0 --non-interactive \
  --credentials '{"target":{"csp":"aws","account":"{...}"}}'

# Non-interactive with credentials file
pyxcloud arch deploy -p 42 -v 0.1.0 --non-interactive \
  --credentials-file creds.json

# Non-interactive using pre-bound accounts from the console
pyxcloud arch deploy -p 42 -v 0.1.0 --non-interactive
```

#### `architecture status`

```bash
pyxcloud arch status -p 42 -v 0.1.0
```

#### `architecture destroy`

```bash
# With confirmation prompt
pyxcloud arch destroy -p 42

# Skip prompt (CI/CD)
pyxcloud arch destroy -p 42 --force
```

---

### `pyxcloud accounts` (alias: `acc`)

Manage cloud provider account bindings — the credentials PyxCloud uses to provision infrastructure.

```bash
# List all account bindings
pyxcloud accounts list

# Create with inline credentials
pyxcloud accounts create --provider aws --credentials '{"access_key_id":"...","secret_access_key":"..."}'

# Create with credentials from a file
pyxcloud accounts create --provider gcp --credentials-file sa-key.json --nickname "prod-gcp"

# Verify credentials are valid
pyxcloud accounts verify --id 42

# Delete an account binding
pyxcloud accounts delete --id 42
pyxcloud accounts delete --id 42 --force
```

Supported providers: `aws`, `azure`, `gcp`, `digitalocean`, `linode`, `ubicloud`, `vultr`, `oracle`, `ibm`, `alibaba`, `stackit`, `ovh`.

---

### `pyxcloud import`

Import existing cloud infrastructure into PyxCloud — the CLI equivalent of the Import Wizard.

#### `import discover`

Scan a cloud account and list all discovered resources.

```bash
pyxcloud import discover --account 42
```

Output includes resource ID, type, name, region, and status — ready for selective import.

#### `import build`

Create a new Build version from discovered resources.

```bash
# Import specific resources by ID
pyxcloud import build --account 42 --project 51 --select vm-abc123,vpc-def456

# Import all discovered resources
pyxcloud import build --account 42 --project 51 --all
```

---

### `pyxcloud keystore` (alias: `keys`)

Manage SSH key associations. Keys are split via Shamir Secret Sharing — one share stays in your database, the other in Vault. Recovery requires step-up re-authentication.

```bash
# List all keys
pyxcloud keystore list

# Create a new key pair
pyxcloud keystore create --label "prod-eu-west"

# Recover a private key (opens browser for re-auth)
pyxcloud keystore recover --id 5
pyxcloud keystore recover --id 5 --output my-key.pem

# Delete a key
pyxcloud keystore delete --id 5
pyxcloud keystore delete --id 5 --force
```

---

### `pyxcloud settings`

Organisation administration — identity, team, roles, seats, and API token lifecycle.

```bash
# Current identity
pyxcloud settings whoami

# Team management (admin)
pyxcloud settings team
pyxcloud settings seats
pyxcloud settings invite --email dev@company.com

# Role management
pyxcloud settings assign-role --user-id <id> --role pyx-developer-role
pyxcloud settings remove-role --user-id <id> --role pyx-audit-role

# API tokens for CI/CD
pyxcloud settings tokens
pyxcloud settings create-token --name "github-actions"
pyxcloud settings revoke-token --id 42
```

Available roles: `pyx-admin-role`, `pyx-developer-role`, `pyx-billing-manager-role`, `pyx-audit-role`.

---

### `pyxcloud proxy` — Local Shell Bridge

The proxy command starts a local WebSocket-to-SSH bridge on `127.0.0.1`. The PyxCloud web console connects to this bridge to open **real, interactive SSH sessions** directly in the browser — no browser plugins, no proprietary agents.

```bash
# Default port (13337)
pyxcloud proxy

# Custom port
pyxcloud proxy --port 9999
```

**How it works:**

1. The console frontend opens a WebSocket to `ws://localhost:13337/ws/ssh`
2. It sends an `init` payload with host, user, and private key
3. The CLI dials SSH to the target server, allocates a PTY (`xterm-256color`), and spawns a shell
4. All I/O is relayed bidirectionally over the WebSocket — including terminal resize events

The bridge binds exclusively to `127.0.0.1` and never exposes itself to the network. Your private key transits only between the browser tab and your local machine.

---

## Shell Autocompletion

Native completions for `bash`, `zsh`, `fish`, and `powershell`. The install script configures this automatically. For manual setup:

```bash
# Bash
source <(pyxcloud completion bash)

# Zsh
source <(pyxcloud completion zsh)

# Fish
pyxcloud completion fish | source

# PowerShell
pyxcloud completion powershell | Out-String | Invoke-Expression
```

To persist, add the relevant line to your shell profile (`~/.bashrc`, `~/.zshrc`, etc.).

---

## Global Flags

| Flag          | Description                              |
|---------------|------------------------------------------|
| `--api-url`   | Override the PyxCloud API endpoint       |
| `--verbose`   | Enable detailed debug output             |
| `--help`      | Show help for any command                |

---

## CI/CD Integration

A typical GitHub Actions workflow:

```yaml
- name: Install PyxCloud CLI
  run: curl -sL https://github.com/PyxCloud/pyxcloud-cli/releases/latest/download/install.sh | bash

- name: Authenticate
  run: pyxcloud auth login --token ${{ secrets.PYXCLOUD_TOKEN }}

- name: Deploy
  run: pyxcloud arch deploy -p ${{ vars.PROJECT_ID }} -v ${{ github.ref_name }} --non-interactive
```

---

## Security Model

- **PKCE OAuth2**: Browser-based login uses Proof Key for Code Exchange — no client secrets stored
- **Offline tokens**: Sessions persist via Keycloak offline tokens with automatic refresh
- **Shamir key recovery**: Private keys are never stored whole — split shares require step-up re-authentication to reconstruct
- **Local-only proxy**: The shell bridge binds to `127.0.0.1` and cannot be reached from the network
- **API tokens**: Opaque `pyxc_*` tokens are exchanged for short-lived JWTs before each API call

---

## License

Copyright © 2026 CumulusCorp Inc. All Rights Reserved.

This software is distributed under a proprietary End User License Agreement (EULA).
Reverse engineering, modification, and unauthorized redistribution are strictly prohibited.
See the [LICENSE](LICENSE) file for details.
