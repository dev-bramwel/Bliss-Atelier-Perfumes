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
      {
        role: "employee",
        canManageInventory: true,
        label: "permitted-employee",
      },
      { role: "customer", canManageInventory: false },
    ]) {
      const role = testCase.role;
      const canManage =
        role === "owner" ||
        (role === "employee" && testCase.canManageInventory);
      const context = await browser.newContext({
        viewport: { width: 1280, height: 850 },
      });
      const page = await context.newPage();
      const errors = [],
        writes = [],
        requests = [];
      let revoked = false,
        offered = null,
        failWrite = false,
        customerActive = role !== "owner";
      const userRoles = {
        "usr-owner": "owner",
        "usr-employee": "employee",
        "usr-customer": "customer",
      };
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
          if (
            endpoint.startsWith("/api/admin/users/") &&
            endpoint.endsWith("/role")
          ) {
            const id = endpoint.split("/")[4];
            userRoles[id] = request.postDataJSON().role;
            return route.fulfill({ json: { status: "updated" } });
          }
          if (endpoint.endsWith("/admin/users/usr-customer/status")) {
            customerActive = request.postDataJSON().is_active;
            return route.fulfill({ json: { status: "updated" } });
          }
          return route.fulfill({ json: { status: "updated" } });
        }
        if (endpoint === "/api/operations/orders")
          return route.fulfill({
            json: {
              orders: [{ ...order, delivery_quote: offered }],
              total: 26,
              pending_dispatch: 1,
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
                  role: userRoles["usr-owner"],
                  is_active: true,
                  created_at: now,
                },
                {
                  id: "usr-employee",
                  name: "Employee",
                  email: "employee@example.test",
                  role: userRoles["usr-employee"],
                  is_active: true,
                  created_at: now,
                },
                {
                  id: "usr-customer",
                  name: "",
                  email: "customer@example.test",
                  role: userRoles["usr-customer"],
                  is_active: customerActive,
                  created_at: "2026-11-20T10:00:00Z",
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
          true,
        );
        assert.equal(
          await page.locator('[data-section="inventory"]').isVisible(),
          canManage,
        );
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
            await page
              .locator('#addInventoryForm button[type="submit"]')
              .textContent(),
            "Confirm restock",
          );
          assert.equal(
            await page
              .locator('#addInventoryForm input[name="quantity"]')
              .getAttribute("min"),
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
          await page.waitForSelector("#usersRows tr");
          assert.deepEqual(
            await page.locator("table:has(#usersRows) th").allTextContents(),
            ["Name", "Email", "Role", "Status", "Created", "Actions"],
          );
          const ownerRow = page
            .locator("#usersRows tr")
            .filter({ hasText: "owner@example.test" });
          const employeeRow = page
            .locator("#usersRows tr")
            .filter({ hasText: "employee@example.test" });
          const customerRow = page
            .locator("#usersRows tr")
            .filter({ hasText: "customer@example.test" });
          const cells = customerRow.locator("td");
          assert.equal(await cells.count(), 6);
          assert.equal(await cells.nth(0).textContent(), "Anonymous Shopper");
          assert.equal(await cells.nth(1).textContent(), "customer@example.test");
          assert.equal(await cells.nth(2).textContent(), "Customer");
          assert.equal(await cells.nth(3).textContent(), "Inactive");
          assert.equal(await cells.nth(4).textContent(), "20 Nov 2026");
          assert.equal(await customerRow.locator(".admin-status-pill.is-inactive").count(), 1);
          assert.equal(await page.locator("#usersRows .admin-status-pill.is-active").count(), 2);
          assert.ok(!(await page.locator("#usersRows").textContent()).includes("usr-"));
          const desktopViewport = page.viewportSize();
          await page.setViewportSize({ width: 390, height: 844 });
          assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1));
          assert.ok(await page.locator("#usersRows").isVisible());
          await page.setViewportSize(desktopViewport);
          assert.equal(await page.locator("#usersRows select").count(), 0);
          assert.equal(
            await ownerRow
              .getByRole("button", { name: /Promote to Employee|Revoke Staff/ })
              .count(),
            0,
          );
          await customerRow
            .getByRole("button", { name: "Promote to Employee" })
            .click();
          await customerRow
            .getByRole("button", { name: "Revoke Staff" })
            .waitFor();
          assert.equal(
            writes.at(-1).endpoint,
            "/api/admin/users/usr-customer/role",
          );
          assert.deepEqual(writes.at(-1).body, { role: "employee" });
          const beforeCancel = writes.length;
          page.once("dialog", (dialog) => dialog.dismiss());
          await employeeRow
            .getByRole("button", { name: "Revoke Staff" })
            .click();
          assert.equal(writes.length, beforeCancel);
          page.once("dialog", (dialog) => dialog.accept());
          await employeeRow
            .getByRole("button", { name: "Revoke Staff" })
            .click();
          await employeeRow
            .getByRole("button", { name: "Promote to Employee" })
            .waitFor();
          assert.equal(
            writes.at(-1).endpoint,
            "/api/admin/users/usr-employee/role",
          );
          assert.deepEqual(writes.at(-1).body, { role: "customer" });
        }
        if (role === "employee") {
          await page.goto(base + "/admin/users");
          await page.waitForSelector("#usersRows tr");

          const usersText = await page.locator("#usersRows").textContent();

          assert.ok(usersText.includes("customer@example.test"));
          assert.ok(!usersText.includes("owner@example.test"));
          assert.ok(!usersText.includes("employee@example.test"));

          assert.equal(await page.locator("#usersRows select").count(), 0);

          assert.equal(await page.locator("#usersRows button").count(), 1);
          await page
            .getByRole("button", { name: "Deactivate", exact: true })
            .click();

          await page
            .getByRole("button", { name: "Activate", exact: true })
            .waitFor();

          assert.equal(customerActive, false);
          assert.ok(
            writes.some(
              (w) =>
                w.endpoint.endsWith("/usr-customer/status") &&
                w.body.is_active === false,
            ),
          );

          await page
            .getByRole("button", { name: "Activate", exact: true })
            .click();

          await page
            .getByRole("button", { name: "Deactivate", exact: true })
            .waitFor();

          assert.equal(customerActive, true);
          assert.ok(
            writes.some(
              (w) =>
                w.endpoint.endsWith("/usr-customer/status") &&
                w.body.is_active === true,
            ),
          );
        }
        await page.setViewportSize({ width: 390, height: 844 });
        await page.screenshot({
          path: path.join(
            artifactDir,
            (testCase.label || role) + "-mobile.png",
          ),
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
      assert.equal(await page.locator('[data-section="audit-logs"]').count(), 0);
      assert.equal(await page.locator("#panelAudit, #auditRows").count(), 0);
      assert.equal(await page.locator('[data-panel="audit-logs"], [data-refresh="audit-logs"]').count(), 0);
      assert.equal(requests.some((p) => p.includes("/admin/audit-logs")), false);
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
