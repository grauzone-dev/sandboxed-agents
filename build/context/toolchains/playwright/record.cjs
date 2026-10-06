const fs = require('node:fs');
const path = require('node:path');
const { version: playwrightVersion } = require('/opt/playwright/node_modules/@playwright/test/package.json');
const { browsers } = require('/opt/playwright/node_modules/playwright-core/browsers.json');

console.log(`playwright\t${playwrightVersion}`);
for (const name of ['chromium', 'chromium-headless-shell', 'firefox', 'webkit', 'ffmpeg']) {
    const browser = browsers.find(download => download.name === name);
    const directory = path.join('/opt/playwright-browsers', `${name.replaceAll('-', '_')}-${browser.revision}`);
    fs.accessSync(path.join(directory, 'INSTALLATION_COMPLETE'));
    const browserVersion = browser.browserVersion ? `${browser.browserVersion} (revision ${browser.revision})` : `revision ${browser.revision}`;
    console.log(`${name}\t${browserVersion}`);
}
