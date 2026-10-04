#!/bin/sh
set -eu

mkdir -p /usr/local/share/sandboxed-agents
{
    dpkg-query -W -f='${binary:Package}\t${Version}\n'
    printf 'node\t%s\n' "$(node --version)"
    printf 'npm\t%s\n' "$(npm --version)"
    printf 'manager\t%s\n' "$(sandboxed-agents-manager version)"
    for recorder in /usr/local/share/sandboxed-agents/versions.d/*.sh; do
        [ -f "$recorder" ] || continue
        sh "$recorder"
    done
} > /usr/local/share/sandboxed-agents/versions.tsv
