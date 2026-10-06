const assert = require('node:assert/strict');
const { chromium, firefox, webkit } = require('/opt/playwright/node_modules/playwright');

(async () => {
    for (const engine of [chromium, firefox, webkit]) {
        const browser = await engine.launch({ headless: true });
        try {
            const page = await browser.newPage();
            await page.setContent('<title>playwright</title>');
            assert.equal(await page.title(), 'playwright');
            console.log(`${engine.name()}\t${browser.version()}`);
        } finally {
            await browser.close();
        }
    }
})().catch(error => {
    console.error(error);
    process.exitCode = 1;
});
