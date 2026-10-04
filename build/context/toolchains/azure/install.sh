#!/bin/sh
set -eu

export DEBIAN_FRONTEND=noninteractive
mkdir -p /etc/apt/keyrings
curl -fsSL https://packages.microsoft.com/keys/microsoft.asc -o /tmp/microsoft.asc
microsoft_key_home=$(mktemp -d)
gpg --batch --yes --homedir "$microsoft_key_home" --dearmor -o /etc/apt/keyrings/microsoft.gpg /tmp/microsoft.asc
rm -rf "$microsoft_key_home" /tmp/microsoft.asc
chmod 0644 /etc/apt/keyrings/microsoft.gpg
printf '%s\n' 'deb [arch=amd64 signed-by=/etc/apt/keyrings/microsoft.gpg] https://packages.microsoft.com/repos/azure-cli/ bookworm main' > /etc/apt/sources.list.d/azure-cli.list
apt-get update
apt-get install -y --no-install-recommends azure-cli
rm -rf /var/lib/apt/lists/*

azure_config=$(mktemp -d)
trap 'rm -rf "$azure_config"' EXIT HUP INT TERM
export AZURE_CONFIG_DIR="$azure_config"
az extension add --system --name azure-devops --allow-preview false
extension_directory=$(/opt/az/bin/python3 -c 'from azure.cli.core.extension import EXTENSIONS_SYS_DIR; print(EXTENSIONS_SYS_DIR)')
chmod -R a+rX "$extension_directory/azure-devops"
