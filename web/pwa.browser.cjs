const assert = require("node:assert/strict");
const { chromium } = require(
    process.env.PLAYWRIGHT_MODULE || "../tools/ci/node_modules/playwright"
);

const base = process.env.BLISS_WEB_URL || "http://127.0.0.1:8086";

async function main() {
    const browser = await chromium.launch({ headless: true });

    try {
        const context = await browser.newContext({
            serviceWorkers: "allow"
        });

        const page = await context.newPage();

        await page.goto(base);

        await page.waitForFunction(
            () => navigator.serviceWorker.controller !== null
        );

        console.log("PASS: Service worker registered and active");

        const cached = await page.evaluate(async () => {
            return Boolean(await caches.match("/offline.html"));
        });

        assert.ok(cached);
        console.log("PASS: Offline fallback cached");

        const assetCached = await page.evaluate(async () => {
            const response = await fetch("/static/style.css");
            return response.ok && Boolean(await caches.match("/static/style.css"));
        });
        assert.ok(assetCached);
        console.log("PASS: Static asset caching");

        const sensitiveCached = await page.evaluate(async () => {
            const keys = await caches.keys();
            for (const key of keys) {
                const cache = await caches.open(key);
                const requests = await cache.keys();
                if (requests.some(request => {
                    const path = new URL(request.url).pathname;
                    return path === "/" || /^\/(api|admin|cart|checkout|dashboard|order)(\/|$)/.test(path);
                })) return true;
            }
            return false;
        });
        assert.equal(sensitiveCached, false);
        console.log("PASS: No sensitive pages or API responses cached");

        await context.setOffline(true);

        await page.goto(`${base}/cart`);

        await page.getByRole("heading", {
            name: "The Scent Is Paused"
        }).waitFor();

        console.log("PASS: Offline navigation fallback");

        await context.setOffline(false);

        await page.getByRole("button", {
            name: "Retry",
            exact: true
        }).click();

        await page.waitForFunction(
            () => !document.querySelector("h1")?.textContent.includes(
                "The Scent Is Paused"
            )
        );

        assert.equal(new URL(page.url()).pathname, "/cart");

        console.log("PASS: Connection recovery and retry");

        await page.goto(base);
        await page.evaluate(() => {
            const event = new Event("beforeinstallprompt", { cancelable: true });
            event.prompt = async () => { window.installPromptCalled = true; };
            event.userChoice = Promise.resolve({ outcome: "accepted" });
            window.dispatchEvent(event);
        });

        await page.locator("#accountMenu summary").click();
        const installButton = page.locator("#pwaInstallButton");
        await installButton.waitFor({ state: "visible" });
        console.log("PASS: Install button displayed");

        await installButton.click();
        assert.ok(await page.evaluate(() => window.installPromptCalled));
        console.log("PASS: Installation prompt triggered");

        await page.evaluate(() => window.dispatchEvent(new Event("appinstalled")));
        await installButton.waitFor({ state: "hidden" });
        console.log("PASS: Install button hidden after installation");

        const iosContext = await browser.newContext({
            userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1",
            viewport: { width: 390, height: 844 },
            isMobile: true,
            hasTouch: true
        });

        const iosPage = await iosContext.newPage();
        await iosPage.goto(base);

        const banner = iosPage.locator("#pwaIOSBanner");
        await banner.waitFor({ state: "visible" });
        console.log("PASS: iOS installation banner displayed");

        await iosPage.locator("#pwaIOSDismiss").click();
        await banner.waitFor({ state: "hidden" });
        console.log("PASS: iOS banner dismissed");

        await iosPage.reload();
        await banner.waitFor({ state: "hidden" });
        assert.equal(await iosPage.evaluate(() => localStorage.getItem("pwa-ios-dismissed")), "true");
        console.log("PASS: iOS dismissal persisted");

        await iosContext.close();

    } finally {
        await browser.close();
    }
}

main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
});
