#!/bin/sh
set -eu

mkdir -p /run/sshd
/usr/local/bin/sandboxed-agents-manager ssh start
exec runuser -u agent -- sleep infinity
