import { chromium } from "playwright";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";

const origin = process.env.SMOKE_ORIGIN || "http://frontend:8080";
const output = process.env.REPORT_DIR || "/reports";
mkdirSync(output, { recursive: true });
const browser = await chromium.launch({ headless: true });
let page;
const errors = [];
try {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
  });
  await context.route("**/*", (route) =>
    new URL(route.request().url()).origin === new URL(origin).origin
      ? route.continue()
      : route.abort(),
  );
  await context.addInitScript(() =>
    localStorage.setItem("cart", JSON.stringify({ p1: 2 })),
  );
  page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  assert.equal((await page.goto(origin + "/index.html")).status(), 200);
  assert.equal((await page.goto(origin + "/cart.html")).status(), 200);
  assert.equal((await page.goto(origin + "/checkout.html")).status(), 200);
  assert(await page.locator("#checkoutForm").isVisible());

  const items = page.locator("#checkoutItems");
  await items.filter({ hasText: "Arabian Wood" }).waitFor({ state: "visible" });
  assert.match(await items.innerText(), /Arabian Wood/);

  const total = page.locator("#checkoutTotal");
  await total.filter({ hasText: /5[,.]?000/ }).waitFor({ state: "visible" });
  assert.match(await total.innerText(), /5[,.]?000/);

  assert.equal(
    await page.locator('meta[name="api-base-url"]').getAttribute("content"),
    "",
  );
  for (const path of ["/api/health/live", "/api/health/ready"]) {
    assert.equal(
      await page.evaluate(async (path) => (await fetch(path)).status, path),
      200,
    );
  }
  assert.equal(
    await page.evaluate(() => getComputedStyle(document.body).overflowX),
    "hidden",
  );
  assert.deepEqual(errors, []);
  await page.screenshot({
    path: output + "/checkout-mobile.png",
    fullPage: true,
  });
  writeFileSync(
    output + "/browser.json",
    JSON.stringify(
      {
        status: "passed",
        viewport: "390x844",
        scope:
          "static cart/checkout and same-origin foundation health; no payment or order submission",
      },
      null,
      2,
    ),
  );
} catch (err) {
  console.error("[browser smoke failure]", err);
  if (page) {
    try {
      await page.screenshot({
        path: output + "/checkout-failure.png",
        fullPage: true,
      });
    } catch (e) {
      console.error("[browser screenshot failure]", e);
    }
  }
  try {
    writeFileSync(
      output + "/browser.json",
      JSON.stringify(
        {
          status: "failed",
          error: err.message,
          stack: err.stack,
          pageErrors: errors,
        },
        null,
        2,
      ),
    );
  } catch {}
  throw err;
} finally {
  await browser.close();
}
