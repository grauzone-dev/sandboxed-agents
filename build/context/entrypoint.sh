#!/bin/sh
set -eu

mkdir -p /run/sshd
exec runuser -u agent -- sleep infinity
