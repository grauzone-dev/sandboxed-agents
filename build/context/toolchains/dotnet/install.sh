#!/bin/sh
set -eu

export DEBIAN_FRONTEND=noninteractive
mkdir -p /etc/apt/keyrings
curl -fsSL https://packages.microsoft.com/keys/microsoft.asc -o /tmp/microsoft-dotnet.asc
microsoft_key_home=$(mktemp -d)
gpg --batch --yes --homedir "$microsoft_key_home" --dearmor -o /etc/apt/keyrings/microsoft-dotnet.gpg /tmp/microsoft-dotnet.asc
rm -rf "$microsoft_key_home" /tmp/microsoft-dotnet.asc
chmod 0644 /etc/apt/keyrings/microsoft-dotnet.gpg
printf '%s\n' 'deb [arch=amd64 signed-by=/etc/apt/keyrings/microsoft-dotnet.gpg] https://packages.microsoft.com/debian/12/prod bookworm main' > /etc/apt/sources.list.d/dotnet.list
apt-get update
apt-get install -y --no-install-recommends dotnet-sdk-8.0 dotnet-sdk-9.0 dotnet-sdk-10.0
rm -rf /var/lib/apt/lists/*
