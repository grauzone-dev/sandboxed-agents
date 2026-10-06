#!/bin/sh
set -eu

test "$(id -u)" = 1000
test "$(id -g)" = 1000
export PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers
node /usr/local/share/sandboxed-agents/smoke/playwright.cjs
