#!/bin/sh
set -eu

export DEBIAN_FRONTEND=noninteractive
npm install --prefix /opt/playwright --save-exact --no-audit --no-fund @playwright/test@1.63.0
chmod 0755 /usr/local/bin/playwright
playwright install --with-deps chromium firefox webkit
chmod -R a+rX /opt/playwright /opt/playwright-browsers
rm -rf /var/lib/apt/lists/* /root/.npm
