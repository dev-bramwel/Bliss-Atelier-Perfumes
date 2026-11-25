// Run against cmd/web with PLAYWRIGHT_MODULE pointing to an installed playwright.
// API fixtures isolate browser behavior; server authorization has separate Go tests.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require(
  process.env.PLAYWRIGHT_MODULE || "../tools/ci/node_modules/playwright",
);
const base = process.env.BLISS_WEB_URL || "http://127.0.0.1:8086";
const artifactDir =
  process.env.BLISS_BROWSER_ARTIFACTS || path.resolve(".local/r19-browser");
fs.mkdirSync(artifactDir, { recursive: true });
const now = new Date().toISOString();
const order = {
  id: "ord-one",
  customerName: "Test buyer",
  customerPhone: "254700111222",
  deliveryLocation: "Nyali",
  deliveryOption: "delivery",
  notes: "<img src=x onerror=alert(1)>",
  paymentStatus: "PENDING",
  fulfillmentStatus: "unfulfilled",
  totalKes: 2500,
  items: [{ productName: "Arabian Wood", qty: 1, subtotalKes: 2500 }],
};
const exception = {
  id: "exc-one",
  orderId: order.id,
  exceptionType: "AMOUNT_MISMATCH",
  status: "open",
  receivedAmountKes: 2000,
  expectedAmountKes: 2500,
  createdAt: now,
};

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const testCase of [
      { role: "owner", canManageInventory: false },
      { role: "employee", canManageInventory: false },
      { role: "employee", canManageInventory: true, label: "permitted-employee" },
      { role: "customer", canManageInventory: false },
    ]) {
      const role = testCase.role;
      const canManage =
        role === "owner" || (role === "employee" && testCase.canManageInventory);
      const context = await browser.newContext({
        viewport: { width: 1280, height: 850 },
      });
      const page = await context.newPage();
      const errors = [],
        writes = [],
        requests = [];
      let revoked = false,
        offered = null,
        failWrite = false;
      page.on("pageerror", (error) => errors.push(error.message));
      await page.route("**/api/**", async (route) => {
        const request = route.request(),
          url = new URL(request.url()),
          endpoint = url.pathname;
        requests.push(endpoint + url.search);
        if (endpoint === "/api/health/live")
          return route.fulfill({ json: { status: "alive" } });
        if (endpoint === "/api/auth/me")
          return route.fulfill({
            status: revoked ? 401 : 200,
            json: revoked
              ? { error: "session revoked" }
              : {
                  user: {
                    id: "usr-" + (testCase.label || role),
                    name: "Test " + (testCase.label || role),
                    email: (testCase.label || role) + "@example.test",
                    role,
                    can_manage_inventory: testCase.canManageInventory,
                  },
                  csrf_token: "csrf-test",
                },
          });
        if (endpoint === "/api/catalog")
          return route.fulfill({
            json: {
              products: [{
                id: "prod-arabian-wood",
                name: "Arabian Wood",
                priceKes: 2500,
                stock: 3,
                size: "50ml",
              }],
            },
          });
        if (endpoint === "/api/orders/counter-test/payment-status")
          return route.fulfill({
            json: {
              paymentStatus: "PAID",
              fulfillmentStatus: "fulfilled",
            },
          });
        if (request.method() === "POST") {
          writes.push({
            endpoint,
            body: request.postDataJSON(),
            csrf: request.headers()["x-csrf-token"],
          });
          if (failWrite) {
            failWrite = false;
            return route.abort("failed");
          }
          if (revoked)
            return route.fulfill({
              status: 401,
              json: { error: "session revoked" },
            });
          assert.equal(
            request.headers()["x-csrf-token"],
            "csrf-test",
            endpoint + " omitted CSRF",
          );
          if (endpoint === "/api/operations/counter-order") {
            assert.ok(request.headers()["idempotency-key"], "Missing idempotency key");
            return route.fulfill({
              status: 201,
              json: {
                order: { id: "counter-test", channel: "counter" },
                quote_required: false,
                stkPush: {},
              },
            });
          }
          if (endpoint.endsWith("/delivery-quote")) {
            offered = {
              status: "pending",
              amount_kes: 350,
              expires_at: new Date(Date.now() + 900000).toISOString(),
            };
            return route.fulfill({ status: 201, json: { quote: offered } });
          }
          if (endpoint.endsWith("/operations/inventory/restock")) {
            return route.fulfill({
              status: 200,
              json: {
                status: "restocked",
                product_id: request.postDataJSON()?.product_id,
                quantity: request.postDataJSON()?.quantity,
              },
            });
          }
          return route.fulfill({ json: { status: "updated" } });
        }
        if (endpoint === "/api/operations/orders")
          return route.fulfill({
            json: {
              orders: [{ ...order, delivery_quote: offered }],
              total: 26,
              pending_dispatch: 1,
              counter_sales: writes.filter(w => w.endpoint === "/api/operations/counter-order").length,
              counter_revenue_kes: writes.filter(w => w.endpoint === "/api/operations/counter-order").length * 5000,
            },
          });
        if (endpoint === "/api/operations/inventory")
          return route.fulfill({
            json: {
              products: [
                {
                  id: "prod-arabian-wood",
                  name: "Arabian Wood",
                  priceKes: 2500,
                  stock: 5,
                  held: 2,
                  available: 3,
                  opened: true,
                },
              ],
              common_price_kes: 2500,
              total: 23,
              approval_until: new Date(Date.now() + 3600000).toISOString(),
            },
          });
        if (endpoint === "/api/orders/ord-one")
          return route.fulfill({ json: { order } });
        if (endpoint.endsWith("/payments/exceptions/exc-one"))
          return route.fulfill({ json: exception });
        if (endpoint.endsWith("/payments/exceptions"))
          return route.fulfill({ json: [exception] });
        if (
          endpoint.endsWith("/payments/unresolved") ||
          endpoint.endsWith("/payments/audit-ledger")
        )
          return route.fulfill({ json: [] });
        if (endpoint.endsWith("/admin/users"))
          return route.fulfill({
            json: {
              users: [
                {
                  id: "usr-owner",
                  name: "Owner",
                  email: "owner@example.test",
                  role: "owner",
                  is_active: true,
                  created_at: now,
                },
                {
                  id: "usr-employee",
                  name: "Employee",
                  email: "employee@example.test",
                  role: "employee",
                  is_active: true,
                  created_at: now,
                },
              ],
            },
          });
        if (endpoint.endsWith("/admin/audit-logs"))
          return route.fulfill({
            json: {
              audit_logs: [
                {
                  actor_role: "owner",
                  action: "owner-secret-event",
                  created_at: now,
                },
                {
                  actor_role: "employee",
                  action: "staff_login",
                  created_at: now,
                  metadata: {},
                },
              ],
            },
          });
        return route.fulfill({
          status: 404,
          json: { error: "unexpected fixture request " + endpoint },
        });
      });
      await page.goto(base + "/admin");
      if (role === "customer") {
        await page.waitForFunction(
          () => !document.getElementById("adminSignIn").hidden,
        );
        assert.equal(await page.locator("#adminWorkspace").isVisible(), false);
        assert.equal(
          requests.some((p) => p.startsWith("/api/operations")),
          false,
        );
      } else {
        await page.waitForSelector("#ordersRows button");
        assert.equal(await page.locator("#staffRole").textContent(), role);
        assert.equal(
          await page.locator('[data-section="users"]').isVisible(),
          role !== "employee",
        );
        assert.equal(
          await page.locator('[data-section="inventory"]').isVisible(),
          canManage,
        );
        if (role === "employee") {
          await page.goto(base + "/admin/counter");
          await page.getByRole("button", { name: "Add to sale" }).click();
          await page.getByRole("button", { name: "Increase Arabian Wood quantity" }).click();
          assert.match(await page.locator("#counterTotal").textContent(), /5,000/);
          await page.locator("#counterCheckoutForm input[name=phone]").fill("0712345678");
          await page.locator("#counterPay").evaluate(button => {
            button.click();
            if (!button.disabled) throw new Error("Checkout button was not disabled");
            button.click();
          });
          await page.waitForSelector("#counterPaymentState[data-kind=success]");
          assert.match(await page.locator("#counterPaymentState").textContent(), /Sale collected successfully/);
          assert.equal(writes.filter(w => w.endpoint === "/api/operations/counter-order").length, 1);
          await page.locator("#counterNewSale").click();
          await page.goto(base + "/admin");
          await page.waitForSelector("#ordersRows button");
          assert.equal(await page.locator("#metricCounterSales").textContent(), "1");
          assert.match(await page.locator("#metricCounterRevenue").textContent(), /5,000/);
        }
        if (canManage) {
          await page.goto(base + "/admin/inventory");
          await page.waitForSelector("#inventoryRows tr");
          assert.equal(
            await page.locator("#btnAddInventory").isVisible(),
            true,
          );
          await page.locator("#btnAddInventory").click();
          await page.waitForSelector("#addInventoryDialog[open]");
          assert.equal(await page.locator("#restockProduct option").count(), 1);
          assert.equal(
            await page.locator('#addInventoryForm button[type="submit"]').textContent(),
            "Confirm restock",
          );
          assert.equal(
            await page.locator('#addInventoryForm input[name="quantity"]').getAttribute("min"),
            "1",
          );
          await page
            .locator('#addInventoryForm select[name="product_id"]')
            .selectOption("prod-arabian-wood");
          await page
            .locator('#addInventoryForm input[name="quantity"]')
            .fill("12");
          await page
            .locator('#addInventoryForm input[name="reference"]')
            .fill("Restock batch INV-001");
          await page.locator('#addInventoryForm button[type="submit"]').click();
          await page.waitForFunction(
            () => !document.getElementById("addInventoryDialog").open,
          );
          assert.ok(
            writes.some(
              (w) =>
                w.endpoint.endsWith("/operations/inventory/restock") &&
                w.body.product_id === "prod-arabian-wood" &&
                w.body.quantity === 12 &&
                w.body.reference === "Restock batch INV-001" &&
                typeof w.body.batch_id === "string" &&
                w.body.batch_id.startsWith("restock_"),
            ),
          );
          await page.goto(base + "/admin");
          await page.waitForSelector("#ordersRows button");
        } else {
          assert.equal(
            await page.locator("#btnAddInventory").isVisible(),
            false,
          );
        }
        if (role === "employee")
          assert.equal(
            requests.some((p) => p.startsWith("/api/admin/")),
            false,
          );
        await page.locator('[data-page-next="orders"]').click();
        await page.waitForFunction(() =>
          document
            .querySelector('[data-page-label="orders"]')
            .textContent.includes("Page 2"),
        );
        assert.ok(requests.some((p) => p.includes("offset=25")));
        await page.locator("#ordersRows button").first().click();
        await page.waitForSelector("#orderDialog[open]");
        assert.ok(
          (await page.locator("#orderDetails").textContent()).includes(
            "<img src=x onerror=alert(1)>",
          ),
        );
        assert.equal(
          await page.locator("#orderDetails img").count(),
          0,
          "customer text became HTML",
        );
        if (role !== "employee") {
          await page.locator('#quoteForm input[name="fee_kes"]').fill("350");
          await page
            .locator("#quoteForm textarea")
            .fill("Draft delivery to Nyali");
          await context.setOffline(true);
          await page.waitForSelector("#offlineOverlay[open]");
          assert.ok(
            (await page.locator("#offlineDescription").textContent()).includes(
              "Atelier management",
            ),
          );
          assert.equal(
            await page.locator("#quoteForm button").isDisabled(),
            true,
          );
          const before = writes.length;
          await context.setOffline(false);
          await page.waitForFunction(
            () => document.getElementById("offlineOverlay").hidden,
            { timeout: 10000 },
          );
          assert.equal(
            await page.locator("#quoteForm textarea").inputValue(),
            "Draft delivery to Nyali",
          );
          assert.equal(writes.length, before, "recovery replayed a write");
          await page.locator("#quoteForm button").click();
          await page.waitForFunction(() =>
            document
              .getElementById("quoteStatus")
              .textContent.includes("offered"),
          );
          assert.deepEqual(writes.at(-1).body, {
            fee_kes: 350,
            notes: "Draft delivery to Nyali",
          });
        } else
          assert.equal(await page.locator("#quoteForm").isVisible(), false);
        await page.locator("#orderDialog [data-close-dialog]").click();
        if (role !== "employee") {
          await page.goto(base + "/admin/payments");
          await page.waitForSelector("#exceptionsRows button");
          await page.locator("#exceptionsRows button").click();
          await page.waitForSelector("#exceptionDialog[open]");
          await page.locator("#resolveForm textarea").fill("Verified reversal");
          await page.locator("#resolveForm input").fill("evidence-one");
          await page.locator("#resolveForm button").click();
          await page.waitForFunction(
            () => !document.getElementById("exceptionDialog").open,
          );
          assert.ok(
            writes.some(
              (w) =>
                w.endpoint.endsWith("/exc-one/resolve") &&
                w.body.resolution_notes === "Verified reversal",
            ),
          );
          await page.locator("#openSettlement").click();
          for (const [name, value] of Object.entries({
            order_id: "ord-one",
            amount_kes: "2500",
            receipt_number: "QWE123RTY",
            evidence: "verified-sms",
            notes: "Counter payment",
          }))
            await page.locator(`#settlementForm [name="${name}"]`).fill(value);
          failWrite = true;
          await page.locator("#settlementForm button").click();
          await page.waitForSelector("#offlineOverlay[open]");
          await page.waitForFunction(
            () => document.getElementById("offlineOverlay").hidden,
            { timeout: 10000 },
          );
          assert.equal(
            await page
              .locator('#settlementForm [name="receipt_number"]')
              .inputValue(),
            "QWE123RTY",
          );
          await page.locator("#settlementForm button").click();
          await page.waitForFunction(
            () => !document.getElementById("settlementDialog").open,
          );
          assert.equal(writes.at(-1).body.amount_kes, 2500);
          await page.goto(base + "/admin/users");
          await page.waitForSelector("#usersRows select");
          assert.equal(
            await page.locator('#usersRows option[value="owner"]').count(),
            0,
          );
          const roleDraft = page.locator("#usersRows select").first();
          await roleDraft.selectOption("customer");
          await context.setOffline(true);
          await page.waitForSelector("#offlineOverlay[open]");
          await context.setOffline(false);
          await page.waitForFunction(
            () => document.getElementById("offlineOverlay").hidden,
          );
          assert.equal(await roleDraft.inputValue(), "customer");
          await page.locator("#usersRows button").first().click();
          await page.waitForFunction(() =>
            document
              .getElementById("adminMessage")
              .textContent.includes("Role updated"),
          );
          await page.goto(base + "/admin/audit-logs");
          await page.waitForSelector("#auditRows tr");
        }
        await page.setViewportSize({ width: 390, height: 844 });
        await page.screenshot({
          path: path.join(artifactDir, (testCase.label || role) + "-mobile.png"),
          fullPage: true,
        });
        assert.ok(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= window.innerWidth + 1,
          ),
          "mobile page overflows",
        );
        revoked = true;
        await page.evaluate(() => window.BlissConnection.dropped());
        await page.evaluate(() => window.BlissConnection.heartbeat());
        // A revoked session must be noticed by a protected request, without replaying writes.
        await page.route("**/api/operations/orders?**", (route) =>
          route.fulfill({ status: 401, json: { error: "revoked" } }),
        );
        await page.goto(base + "/admin");
        await page.waitForSelector("#adminSignIn:not([hidden])");
      }
      assert.deepEqual(errors, [], (testCase.label || role) + " browser errors");
      console.log("PASS admin browser:", testCase.label || role);
      await context.close();
    }
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
