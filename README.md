# PyxCloud CLI

The official command-line interface for PyxCloud Platform.
The CLI provides a unified tool to deploy architectures, manage your organisation, retrieve secrets, and securely interact with the PyxCloud control plane.

## Installation

### macOS and Linux
The fastest way to install (or update) the CLI on macOS and Linux is using our universal installer script. It automatically detects your OS and architecture, downloads the latest binary, and configures autocomplete.

```bash
curl -sL https://pyxcloud.io/install.sh | bash
```

Alternatively, if you use Homebrew:
```bash
brew tap pyxcloud/tap
brew install pyxcloud
```

### Windows
On Windows, you can install the CLI using Scoop:

```powershell
scoop bucket add pyxcloud https://github.com/pyxcloud/scoop-bucket.git
scoop install pyxcloud
```

## Enable Auto-completion

The CLI natively supports auto-completion for `bash`, `zsh`, `fish`, and `powershell`.
The `install.sh` script attempts to automatically inject completion scripts into your `~/.bashrc` or `~/.zshrc`. 

If you prefer to configure it manually, add the following to your shell configuration file:

**Bash:**
```bash
source <(pyxcloud completion bash)
```

**Zsh:**
```zsh
source <(pyxcloud completion zsh)
```

## Getting Started

1. **Login to your account:**
   ```bash
   pyxcloud auth login
   ```
   *This will open your browser to authenticate via PyxCloud SSO.*

2. **Check your identity:**
   ```bash
   pyxcloud settings whoami
   ```

3. **Deploy an architecture:**
   ```bash
   pyxcloud deploy
   ```

## License
Copyright © 2026 PyxCloud. All Rights Reserved.
This binary is distributed under a proprietary End User License Agreement (EULA). Reverse engineering, modification, and unauthorized redistribution are strictly prohibited. See the [LICENSE](LICENSE) file for more details.
