const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const browsers = require(
  process.env.PLAYWRIGHT_MODULE || "../tools/ci/node_modules/playwright",
);
const base = process.env.BLISS_WEB_URL || "http://127.0.0.1:8086";
const artifacts = process.env.BLISS_BROWSER_ARTIFACTS || ".local/r20-browser";
fs.mkdirSync(artifacts, { recursive: true });
const product = {
  id: "p1",
  name: "Arabian Wood",
  category: "Woody",
  gender: "Unisex",
  size: "100ml",
  notes: "Warm woods",
  description: "Perfume",
  images: ["img/arabian%20wood.jpeg"],
  priceKes: 2500,
  available: true,
  stock: 7,
};
let lastPage;
async function fixture(browser, authenticated = false) {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
  });
  const state = {
    created: false,
    quote: null,
    option: "delivery",
    payment: "PENDING",
    attempt: null,
    receipt: "",
    expired: false,
    loseCreate: false,
    loseRetry: false,
    creates: [],
    retries: [],
    mutations: [],
    polls: 0,
    auth: authenticated,
    deny: false,
  };
  await context.addInitScript(() => {
    if (!localStorage.getItem("cart"))
      localStorage.setItem("cart", JSON.stringify({ p1: 1 }));
  });
  const page = await context.newPage(),
    errors = [];
  lastPage = page;
  await context.route(/fonts\.(googleapis|gstatic)\.com/, (route) =>
    route.abort(),
  );
  page.on("pageerror", (e) => errors.push(String(e)));
  await context.route("**/api/**", async (route) => {
    const req = route.request(),
      url = new URL(req.url()),
      endpoint = url.pathname;
    const response = (data, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        headers: { date: new Date().toUTCString() },
        body: JSON.stringify(data),
      });
    if (endpoint === "/api/health/live") return response({ status: "alive" });
    if (endpoint === "/api/catalog") return response({ products: [product] });
    if (endpoint === "/api/auth/me")
      return response(
        state.auth
          ? {
              user: {
                id: "usr-customer",
                name: "Test Shopper",
                role: "customer",
              },
              csrf_token: "csrf-fixture",
            }
          : { error: "unauthenticated" },
        state.auth ? 200 : 401,
      );
    if (req.method() === "POST") {
      state.mutations.push(endpoint);
      if (state.auth)
        assert.equal(req.headers()["x-csrf-token"], "csrf-fixture");
    }
    if (endpoint === "/api/auth/logout") {
      state.auth = false;
      return response({ status: "ok" });
    }
    const hold = new Date(
      Date.now() + (state.expired ? -1000 : 900000),
    ).toISOString();
    const order = {
      id: "ord-fixture",
      payment_status: state.payment,
      delivery_option: state.option,
      total_kes: 2500,
      hold_expires_at: hold,
      items: [
        { product_name: "Arabian Wood", quantity: 1, subtotal_kes: 2500 },
      ],
      latest_attempt: state.attempt,
      payable_breakdown:
        state.option === "pickup" ? { payable_merchant_kes: 2500 } : undefined,
    };
    if (endpoint === "/api/orders" && req.method() === "POST") {
      state.created = true;
      state.creates.push({
        key: req.headers()["idempotency-key"],
        body: req.postData(),
      });
      assert.match(state.creates.at(-1).key, /^[0-9a-f-]{36}$/);
      const payload = req.postDataJSON();
      assert.equal(payload.customer.phone, "254712345678");
      assert.deepEqual(payload.items, [{ id: "p1", qty: 1 }]);
      state.option = payload.customer.delivery;
      if (state.loseCreate) {
        state.loseCreate = false;
        return route.abort("failed");
      }
      return response(
        {
          order: {
            ...order,
            deliveryOption: state.option,
            reservation: { expiresAt: hold },
          },
          guest_token: "gt_fixture",
          quote_required: state.option === "delivery",
        },
        201,
      );
    }
    if (endpoint.startsWith("/api/orders/")) {
      if (state.deny) return response({ error: "access denied" }, 403);
      if (!state.auth)
        assert.equal(req.headers()["x-guest-token"], "gt_fixture");
      if (endpoint.endsWith("/payment-status")) {
        state.polls++;
        return response({
          paymentStatus: state.payment,
          latestAttempt: state.attempt,
          receiptNumber: state.receipt,
          expiresAt: hold,
          isExpired: state.expired,
        });
      }
      if (endpoint.endsWith("/attempts"))
        return response({ attempts: state.attempt ? [state.attempt] : [] });
      if (
        endpoint.endsWith("/delivery-quote/accept") ||
        endpoint.endsWith("/delivery-quote/reject")
      ) {
        assert.equal(req.postDataJSON().version, state.quote.version);
        state.quote.status = endpoint.endsWith("/accept")
          ? "accepted"
          : "rejected";
        return response({ quote: state.quote });
      }
      if (endpoint.endsWith("/delivery-quote"))
        return state.quote
          ? response({
              quote: state.quote,
              breakdown:
                state.quote.status === "accepted"
                  ? { payable_merchant_kes: 2850, customer_direct_kes: 0 }
                  : undefined,
            })
          : response({ error: "not quoted" }, 404);
      if (endpoint.endsWith("/pickup")) {
        state.option = "pickup";
        return response({ delivery_option: "pickup" });
      }
      if (endpoint.endsWith("/retry-payment")) {
        state.retries.push(req.headers()["idempotency-key"]);
        state.payment = "PENDING";
        state.attempt = {
          id: "att-1",
          attemptNumber: 1,
          status: "accepted",
          amountKes: state.option === "pickup" ? 2500 : 2850,
          phone: "254712345678",
          createdAt: new Date().toISOString(),
        };
        if (state.loseRetry) {
          state.loseRetry = false;
          return route.abort("failed");
        }
        return response({ attempt: state.attempt }, 201);
      }
      return response({ order });
    }
    return response({ error: "unhandled fixture endpoint " + endpoint }, 404);
  });
  return { context, page, state, errors };
}
async function fill(page) {
  await page.goto(base + "/checkout");
  await page.locator("#checkoutTotal").filter({ hasText: "2500" }).waitFor();
  await page.fill("#customerName", "Test Shopper");
  await page.fill("#customerPhone", "0712 345 678");
  await page.check("input[name=delivery][value=delivery]");
  await page.fill("#customerLocation", "Nyali Links Road");
  await page.fill("#customerNotes", "Gift wrapping please");
}
async function offered(f) {
  f.state.quote = {
    version: 1,
    amount_kes: 350,
    fee_recipient: "merchant",
    arrangement: "Bodaboda to Nyali",
    status: "pending",
    expires_at: new Date(Date.now() + 600000).toISOString(),
  };
  await f.page.click("#refreshOrder");
  await f.page.waitForSelector("#acceptQuote:visible");
}
(async () => {
  for (const engine of (
    process.env.BLISS_BROWSER_ENGINES || "chromium,firefox,webkit"
  ).split(",")) {
    const browser = await browsers[engine].launch({ headless: true });
    try {
      // A lost order response must reuse the durable key/body, even after reload and cart edits.
      const f = await fixture(browser);
      await fill(f.page);
      f.state.loseCreate = true;
      await f.page.click("#placeOrder");
      await f.page.waitForSelector("#offlineOverlay[open]");
      assert.equal(
        await f.page.inputValue("#customerNotes"),
        "Gift wrapping please",
      );
      assert.equal(
        await f.page.evaluate(
          () => JSON.parse(localStorage.getItem("cart")).p1,
        ),
        1,
      );
      await f.page.waitForFunction(
        () => document.getElementById("offlineOverlay").hidden,
      );
      await f.page.reload();
      await f.page.waitForSelector("#placeOrder:enabled");
      await f.page.click("#placeOrder");
      await f.page.waitForURL("**/order/ord-fixture");
      assert.equal(f.state.creates.length, 2);
      assert.deepEqual(f.state.creates[0], f.state.creates[1]);
      await f.page.waitForFunction(() =>
        document
          .getElementById("quoteState")
          .textContent.includes("being calculated"),
      );
      assert.equal(
        await f.page.evaluate(() => localStorage.getItem("bliss_guest_token")),
        "gt_fixture",
      );
      assert.equal(
        await f.page.evaluate(
          () => JSON.parse(localStorage.getItem("cart")).p1,
        ),
        1,
      );
      await f.page.reload();
      await offered(f);
      await f.page.click("#acceptQuote");
      await f.page.waitForSelector("#payOrder:visible");
      assert.match(await f.page.textContent("#payableTotal"), /2,850/);
      f.state.loseRetry = true;
      await f.page.click("#payOrder");
      await f.page.waitForSelector("#offlineOverlay[open]");
      await f.page.waitForFunction(
        () => document.getElementById("offlineOverlay").hidden,
      );
      await f.page.waitForFunction(() =>
        document
          .getElementById("paymentTitle")
          .textContent.includes("Check your phone"),
      );
      await f.page.waitForTimeout(2800);
      assert.equal(
        f.state.retries.length,
        1,
        "reconnection must not send another STK",
      );
      f.state.attempt = {
        ...f.state.attempt,
        status: "failed",
        errorMessage: "User cancelled transaction",
      };
      f.state.payment = "FAILED";
      await f.page.click("#refreshOrder");
      await f.page.waitForSelector("#payOrder:visible");
      assert.match(
        await f.page.textContent("#paymentMessage"),
        /User cancelled/,
      );
      const polls = f.state.polls;
      await f.page.waitForTimeout(3000);
      assert.equal(f.state.polls, polls, "FAILED must stop polling");
      // A confirmed failed attempt permits a new key; the uncertain previous request is recovered with its old key first.
      await f.page.click("#payOrder");
      await f.page.waitForFunction(() =>
        document
          .getElementById("paymentTitle")
          .textContent.includes("Check your phone"),
      );
      assert.notEqual(
        f.state.retries[0],
        f.state.retries[1],
        "a server-confirmed failed attempt permits a new payment key",
      );
      f.state.payment = "PAID";
      f.state.receipt = "QWE123RTY";
      f.state.attempt = { ...f.state.attempt, status: "succeeded" };
      await f.page.click("#refreshOrder");
      await f.page.waitForFunction(
        () =>
          document.getElementById("paymentTitle").textContent ===
          "Payment confirmed",
      );
      assert.match(await f.page.textContent("#paymentReceipt"), /QWE123RTY/);
      assert.deepEqual(
        await f.page.evaluate(() => JSON.parse(localStorage.getItem("cart"))),
        {},
      );
      await f.page.click("#orderJourney summary");
      await f.page.click("#refreshAttempts");
      await f.page.waitForFunction(() =>
        document
          .getElementById("attemptsHistory")
          .textContent.includes("Attempt 1"),
      );
      assert.match(await f.page.textContent("#attemptsHistory"), /Attempt 1/);
      await f.page.evaluate(() => window.scrollTo(0, 0));
      await f.page.screenshot({
        path: path.join(artifacts, engine + "-paid-mobile.png"),
        fullPage: true,
      });
      assert.deepEqual(f.errors, []);
      await f.context.close();
      // Real browser offline events retain form values, then rejected quotes can become pickup on the same order.
      const g = await fixture(browser, true);
      await fill(g.page);
      await g.context.setOffline(true);
      await g.page.waitForSelector("#offlineOverlay[open]");
      assert.equal(await g.page.isDisabled("#placeOrder"), true);
      await g.context.setOffline(false);
      await g.page.waitForFunction(
        () => document.getElementById("offlineOverlay").hidden,
      );
      assert.equal(
        await g.page.inputValue("#customerLocation"),
        "Nyali Links Road",
      );
      await g.page.evaluate(() => {
        document
          .getElementById("checkoutForm")
          .dispatchEvent(new Event("submit", { cancelable: true }));
        document
          .getElementById("checkoutForm")
          .dispatchEvent(new Event("submit", { cancelable: true }));
      });
      await g.page.waitForURL("**/order/ord-fixture");
      assert.equal(g.state.creates.length, 1);
      await offered(g);
      await g.page.fill("#quoteReason", "Prefer pickup");
      await g.page.click("#rejectQuote");
      await g.page.waitForFunction(() =>
        document.getElementById("quoteState").textContent.includes("declined"),
      );
      await g.page.click("#switchPickup");
      await g.page.waitForFunction(
        () => document.getElementById("quoteCard").hidden,
      );
      assert.match(await g.page.textContent("#payableTotal"), /2,500/);
      assert.equal(g.state.creates.length, 1);
      g.state.expired = true;
      await g.page.click("#refreshOrder");
      await g.page.waitForSelector("#restartOrder:visible");
      assert.equal(await g.page.isVisible("#payOrder"), false);
      assert.equal(
        await g.page.evaluate(
          () => JSON.parse(localStorage.getItem("cart")).p1,
        ),
        1,
      );
      await g.page.setViewportSize({ width: 1440, height: 900 });
      await g.page.evaluate(() => window.scrollTo(0, 0));
      await g.page.screenshot({
        path: path.join(artifacts, engine + "-expired-desktop.png"),
        fullPage: true,
      });
      assert.equal(
        await g.page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
        true,
      );
      await g.page.click("#shopperLogout");
      await g.page.waitForFunction(
        () => document.getElementById("shopperSignIn").hidden === false,
      );
      assert.equal(
        g.state.mutations.filter((p) => p === "/api/orders").length,
        1,
      );
      assert.deepEqual(g.errors, []);
      await g.context.close();
      console.log("PASS shopper browser:", engine);
    } catch (error) {
      if (lastPage && !lastPage.isClosed()) {
        console.error(
          "Browser state:",
          await lastPage.url(),
          await lastPage.locator("#checkoutMessage").textContent(),
        );
        await lastPage.screenshot({
          path: path.join(artifacts, engine + "-failure.png"),
          fullPage: true,
        });
      }
      throw error;
    } finally {
      await browser.close();
    }
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
