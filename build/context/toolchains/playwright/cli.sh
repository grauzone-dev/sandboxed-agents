#!/bin/sh
set -eu

export PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers
exec node /opt/playwright/node_modules/@playwright/test/cli.js "$@"
