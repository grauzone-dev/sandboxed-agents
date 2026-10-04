#!/bin/sh
set -eu

azure_config=$(mktemp -d)
trap 'rm -rf "$azure_config"' EXIT HUP INT TERM
export AZURE_CONFIG_DIR="$azure_config"
version=$(az version --output json | jq -er '.extensions."azure-devops"')
printf 'azure-devops\t%s\n' "$version"
