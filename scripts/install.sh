#!/usr/bin/env bash
set -e

# PyxCloud CLI Universal Installer
# curl -sL https://pyxcloud.io/install.sh | bash

VERSION="latest"
REPO="pyxcloud/pyxcloud-cli"

echo "☁️  Installing PyxCloud CLI..."

# OS detection
OS="$(uname -s)"
case "${OS}" in
    Linux*)     OS_NAME="Linux";;
    Darwin*)    OS_NAME="Darwin";;
    *)          echo "Unsupported OS: ${OS}"; exit 1;;
esac

# Arch detection
ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)     ARCH_NAME="x86_64";;
    i386)       ARCH_NAME="i386";;
    arm64)      ARCH_NAME="arm64";;
    aarch64)    ARCH_NAME="arm64";;
    *)          echo "Unsupported Architecture: ${ARCH}"; exit 1;;
esac

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/pyxcloud_${OS_NAME}_${ARCH_NAME}.tar.gz"
else
  URL="https://github.com/${REPO}/releases/download/v${VERSION}/pyxcloud_${OS_NAME}_${ARCH_NAME}.tar.gz"
fi

echo "⬇️  Downloading from ${URL}..."
TMP_DIR=$(mktemp -d)
curl -sL "${URL}" -o "${TMP_DIR}/pyxcloud.tar.gz"

echo "📦 Extracting archive..."
tar -xzf "${TMP_DIR}/pyxcloud.tar.gz" -C "${TMP_DIR}"

if [ ! -f "${TMP_DIR}/pyxcloud" ]; then
    echo "❌ Download failed or architecture not matched. Run native install: https://pyxcloud.io/docs"
    rm -rf "${TMP_DIR}"
    exit 1
fi

DEST_DIR="/usr/local/bin"
echo "🔑 Moving binary to ${DEST_DIR} (sudo privileges may be requested)..."
if [ -w "$DEST_DIR" ]; then
    mv "${TMP_DIR}/pyxcloud" "${DEST_DIR}/pyxcloud"
else
    sudo mv "${TMP_DIR}/pyxcloud" "${DEST_DIR}/pyxcloud"
fi
chmod +x "${DEST_DIR}/pyxcloud"

# Install Autocompletions dynamically using the valid binary we just deposited
echo "🧩 Configuring command autocompletions..."
if [ "$OS_NAME" = "Linux" ] && [ -d "/etc/bash_completion.d" ]; then
    if [ -w "/etc/bash_completion.d" ]; then
        ${DEST_DIR}/pyxcloud completion bash > /etc/bash_completion.d/pyxcloud 2>/dev/null || true
    else
        sudo bash -c "${DEST_DIR}/pyxcloud completion bash > /etc/bash_completion.d/pyxcloud" 2>/dev/null || true
    fi
elif [ "$OS_NAME" = "Darwin" ]; then
    if command -v brew &> /dev/null; then
        BREW_COMP_DIR="$(brew --prefix)/etc/bash_completion.d"
        if [ -d "$BREW_COMP_DIR" ]; then
            ${DEST_DIR}/pyxcloud completion bash > "$BREW_COMP_DIR/pyxcloud" 2>/dev/null || true
        fi
        
        ZSH_COMP_DIR="$(brew --prefix)/share/zsh/site-functions"
        if [ -d "$ZSH_COMP_DIR" ]; then
             ${DEST_DIR}/pyxcloud completion zsh > "$ZSH_COMP_DIR/_pyxcloud" 2>/dev/null || true
        fi
    fi
fi

rm -rf "${TMP_DIR}"
echo "✅ PyxCloud CLI installed successfully!"
echo "   Run 'pyxcloud --help' to get started."
