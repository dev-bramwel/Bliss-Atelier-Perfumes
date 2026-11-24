/* Session-backed administrative workspace. Drafts live only in the current DOM. */
(() => {
  const connection = window.BlissConnection;
  if (!connection)
    throw new ReferenceError("shared.js must load before dashboard.js");
  const $ = (id) => document.getElementById(id);
  if (!$("adminApp")) return;
  const context = (window.BlissContext = { user: null, csrfToken: "" });
  const pageSize = 25;
  const pages = {};
  let section = "orders",
    busy = false,
    selectedOrder = null,
    selectedException = null,
    inventoryProducts = [];
  const owner = () => context.user?.role === "owner";
  const canManageInventory = () =>
    owner() ||
    (context.user?.role === "employee" &&
      Boolean(context.user?.can_manage_inventory));
  const money = (n) => `KES ${Number(n || 0).toLocaleString("en-KE")}`;
  const date = (s) =>
    s
      ? new Date(s).toLocaleString("en-KE", { timeZone: "Africa/Nairobi" })
      : "—";
  const el = (tag, text) => {
    const node = document.createElement(tag);
    if (text !== undefined) node.textContent = String(text ?? "—");
    return node;
  };
  function message(text, kind = "error") {
    const targets = [
      $("adminMessage"),
      ...document.querySelectorAll("[data-dialog-message]"),
    ];
    targets.forEach((node) => {
      node.textContent = text;
      node.dataset.kind = kind;
      node.hidden = !text;
    });
  }
  function guard() {
    document.querySelectorAll("[data-mutation]").forEach((button) => {
      button.disabled = !context.user || busy || connection.offline;
    });
    connection.protectButtons();
  }
  function endSession(reason) {
    context.user = null;
    context.csrfToken = "";
    $("adminWorkspace").hidden = true;
    $("adminSignIn").hidden = false;
    $("staffName").textContent = "Sign in required";
    $("staffEmail").textContent = "";
    $("staffRole").textContent = "";
    $("adminLogout").hidden = true;
    $("signInReason").textContent = reason;
    document
      .querySelectorAll(".admin-dialog[open]")
      .forEach((dialog) => dialog.close());
    guard();
  }
  async function api(path, options = {}) {
    const mutation = options.method && options.method !== "GET";
    if (mutation && (connection.offline || !context.csrfToken))
      throw new Error(
        "Reconnect and sign in before submitting. Your draft is retained.",
      );
    let response;
    let text = "";
    try {
      response = await fetch(`/api${path}`, {
        credentials: "same-origin",
        cache: "no-store",
        signal: AbortSignal.timeout(15000),
        ...options,
        headers: {
          Accept: "application/json",
          ...(mutation
            ? {
                "Content-Type": "application/json",
                "X-CSRF-Token": context.csrfToken,
              }
            : {}),
          ...options.headers,
        },
      });
      text = await response.text();
    } catch {
      connection.dropped();
      throw new Error(
        mutation
          ? "The operation could not be confirmed. Your draft is retained; refresh the server record before retrying."
          : "Connection lost. Your inputs are retained while we reconnect.",
      );
    }
    let data;
    try {
      data = text ? JSON.parse(text) : {};
    } catch {
      data = {};
    }
    if (response.status === 401) {
      endSession(
        "Your session has ended or staff access is required. Please sign in again.",
      );
      throw new Error("Please sign in again to continue.");
    }
    if (!response.ok) {
      if (response.status === 502 || response.status === 504)
        connection.dropped();
      throw new Error(
        data.error ||
          (response.status === 403
            ? "Your role does not permit this action. Refresh your session if your permissions changed."
            : `Request failed (${response.status}). Your draft is retained.`),
      );
    }
    return data;
  }
  function cell(row, value) {
    const td = el("td");
    if (value?.nodeType) td.append(value);
    else td.textContent = String(value ?? "—");
    row.append(td);
    return td;
  }
  function action(label, fn, mutation = false) {
    const button = el("button", label);
    button.type = "button";
    if (mutation) button.dataset.mutation = "";
    button.addEventListener("click", () => {
      Promise.resolve()
        .then(fn)
        .catch((error) => message(error.message));
    });
    return button;
  }
  function table(id, data, render, columns) {
    const body = $(id);
    body.replaceChildren();
    for (const item of data || []) {
      const row = el("tr");
      render(row, item);
      body.append(row);
    }
    if (!data?.length) {
      const row = el("tr"),
        td = el("td", "No records on this page.");
      td.colSpan = columns;
      row.append(td);
      body.append(row);
    }
    guard();
  }
  function pager(name, count, total) {
    const state = (pages[name] ||= { offset: 0 });
    const root = document.querySelector(`[data-pager="${name}"]`);
    if (!root) return;
    root.querySelector("[data-page-label]").textContent =
      `Page ${state.offset / pageSize + 1}${total !== undefined ? ` · ${total} records` : ""}`;
    root.querySelector("[data-page-prev]").disabled = state.offset === 0;
    root.querySelector("[data-page-next]").disabled =
      total !== undefined ? state.offset + pageSize >= total : count < pageSize;
  }
  const query = (name) => {
    const state = (pages[name] ||= { offset: 0 });
    return `limit=${pageSize}&offset=${state.offset}`;
  };
  function updated() {
    $("adminUpdated").textContent =
      `Last refreshed ${date(new Date())}. Stock and orders refresh every 15 seconds.`;
  }
  async function loadOrders() {
    const data = await api(`/operations/orders?${query("orders")}`);
    table(
      "ordersRows",
      data.orders,
      (row, order) => {
        cell(row, order.id);
        cell(
          row,
          `${order.customerPhone} · ${order.deliveryLocation || order.deliveryOption}`,
        );
        cell(row, money(order.totalKes));
        cell(row, order.paymentStatus);
        cell(row, order.fulfillmentStatus);
        const quote = order.delivery_quote;
        cell(
          row,
          quote
            ? `${quote.status === "pending" ? "offered" : quote.status} · ${money(quote.amount_kes)}`
            : "none",
        );
        cell(
          row,
          action("Inspect order", () => inspectOrder(order.id, quote)),
        );
      },
      7,
    );
    $("metricDispatch").textContent = data.pending_dispatch;
    pager("orders", data.orders?.length || 0, data.total);
    updated();
  }
  async function loadInventory() {
    if (!canManageInventory()) return;
    const data = await api(`/operations/inventory?${query("inventory")}`);
    inventoryProducts = data.products || [];
    $("metricCatalog").textContent = data.total;
    $("metricPrice").textContent = money(data.common_price_kes);
    $("inventoryApproval").textContent = data.approval_until
      ? `Stock approval expires ${date(data.approval_until)}.`
      : "Stock approval is required before availability can be published.";
    table(
      "inventoryRows",
      data.products,
      (row, p) => {
        [
          p.name,
          money(p.priceKes),
          p.stock,
          p.held,
          p.available,
          p.opened ? "Recorded" : "Required",
        ].forEach((value) => cell(row, value));
      },
      6,
    );
    pager("inventory", data.products?.length || 0, data.total);
    updated();
  }
  async function loadExceptions() {
    const data = await api(
      `/admin/payments/exceptions?status=${encodeURIComponent($("exceptionFilter").value)}&${query("exceptions")}`,
    );
    table(
      "exceptionsRows",
      data,
      (row, e) => {
        [
          date(e.createdAt),
          e.exceptionType,
          `${e.orderId || "Unmapped"} · ${e.receiptNumber || "—"}`,
          `${money(e.receivedAmountKes)} / ${money(e.expectedAmountKes)}`,
          e.status,
        ].forEach((v) => cell(row, v));
        cell(
          row,
          action("Inspect exception", () => inspectException(e.id)),
        );
      },
      6,
    );
    pager("exceptions", data?.length || 0);
  }
  async function loadUnresolved() {
    const data = await api(`/admin/payments/unresolved?${query("unresolved")}`);
    table(
      "unresolvedRows",
      data,
      (row, p) =>
        [
          p.orderId,
          money(p.amountKes),
          p.status,
          p.queryCount,
          date(p.nextQueryAt),
        ].forEach((v) => cell(row, v)),
      5,
    );
    pager("unresolved", data?.length || 0);
  }
  async function loadPaymentAudit() {
    const data = await api(
      `/admin/payments/audit-ledger?${query("paymentAudit")}`,
    );
    table(
      "paymentAuditRows",
      data,
      (row, p) =>
        [
          date(p.createdAt),
          `${p.orderId} · ${p.action}`,
          `${money(p.amountKes)} · ${p.receiptNumber}`,
          p.authorizedBy,
          `${p.evidence} · ${p.notes}`,
        ].forEach((v) => cell(row, v)),
      5,
    );
    pager("paymentAudit", data?.length || 0);
  }
  async function loadUsers() {
    const data = await api(`/admin/users?${query("users")}`);
    // The server filters before pagination. This also protects UI controls if an
    // older server accidentally returns an owner to a non-owner.
    const users = (data.users || []).filter(
      (u) => owner() || ["employee", "customer"].includes(u.role),
    );
    table(
      "usersRows",
      users,
      (row, u) => {
        [
          u.id,
          `${u.name} · ${u.email}`,
          u.role,
          u.is_active ? "Yes" : "No",
          date(u.created_at),
        ].forEach((v) => cell(row, v));
        const controls = el("div");
        if (owner()) {
          const select = el("select");
          select.setAttribute(
            "aria-label",
            `New role for ${u.name || u.email}`,
          );
          for (const role of ["employee", "customer"]) {
            const option = el("option", role);
            option.value = role;
            select.append(option);
          }
          if ([...select.options].some((option) => option.value === u.role))
            select.value = u.role;
          controls.append(
            select,
            action(
              "Update role",
              () =>
                mutate(
                  `/admin/users/${encodeURIComponent(u.id)}/role`,
                  { role: select.value },
                  "Role updated; the affected account must sign in again.",
                  loadUsers,
                ),
              true,
            ),
          );
          if (u.role === "employee") {
            const permLabel = el("label");
            const permCheckbox = el("input");
            permCheckbox.type = "checkbox";
            permCheckbox.checked = Boolean(u.can_manage_inventory);
            permCheckbox.setAttribute(
              "aria-label",
              `Can manage inventory for ${u.name || u.email}`,
            );
            permLabel.append(permCheckbox, " Can manage inventory");
            controls.append(
              permLabel,
              action(
                "Save permission",
                () =>
                  mutate(
                    `/admin/users/${encodeURIComponent(u.id)}/permissions`,
                    { can_manage_inventory: permCheckbox.checked },
                    "Inventory permission updated.",
                    loadUsers,
                  ),
                true,
              ),
            );
          }
        }
        controls.append(
          action(
            u.is_active ? "Deactivate" : "Activate",
            () =>
              mutate(
                `/admin/users/${encodeURIComponent(u.id)}/status`,
                { is_active: !u.is_active },
                "Account status updated.",
                loadUsers,
              ),
            true,
          ),
        );
        cell(row, controls);
      },
      6,
    );
    pager("users", data.users?.length || 0);
  }
  const loaders = {
    orders: loadOrders,
    inventory: loadInventory,
    exceptions: loadExceptions,
    unresolved: loadUnresolved,
    paymentAudit: loadPaymentAudit,
    users: loadUsers,
    payments: async () => {
      await Promise.all([
        loadExceptions(),
        loadUnresolved(),
        loadPaymentAudit(),
      ]);
    },
  };
  async function refresh() {
    if (!context.user || connection.offline) return;
    await loaders[section]();
  }
  function quoteCountdown(quote) {
    if (!quote) {
      $("quoteStatus").textContent = "No delivery quote issued.";
      return;
    }
    const remaining = Math.max(
      0,
      Math.ceil((Date.parse(quote.expires_at) - Date.now()) / 1000),
    );
    const status =
      quote.status === "pending"
        ? remaining
          ? "offered"
          : "expired"
        : quote.status;
    $("quoteStatus").textContent =
      `${status} · ${money(quote.amount_kes)} · ${remaining ? `${Math.floor(remaining / 60)}m ${remaining % 60}s remaining` : "quote expiry reached"}`;
  }
  function details(target, fields) {
    const dl = el("dl");
    for (const [label, value] of fields)
      dl.append(el("dt", label), el("dd", value));
    $(target).replaceChildren(dl);
  }
  async function inspectOrder(id, knownQuote) {
    const data = await api(`/orders/${encodeURIComponent(id)}`);
    // Reopening a different entity is an explicit navigation. Background refresh
    // and connection recovery never reset the operator's current forms.
    if (selectedOrder?.id !== id) {
      $("quoteForm").reset();
      $("assignForm").reset();
    }
    selectedOrder = { ...data.order, quote: knownQuote || null };
    details("orderDetails", [
      ["Order", id],
      [
        "Customer",
        `${selectedOrder.customerName} · ${selectedOrder.customerPhone}`,
      ],
      ["Location", selectedOrder.deliveryLocation],
      ["Notes", selectedOrder.notes || "None"],
      ["Payment", selectedOrder.paymentStatus],
      ["Fulfillment", selectedOrder.fulfillmentStatus],
      [
        "Items",
        (selectedOrder.items || [])
          .map(
            (item) =>
              `${item.productName || item.name} × ${item.qty} (${money(item.subtotalKes)})`,
          )
          .join("; "),
      ],
      ["Total", money(selectedOrder.totalKes)],
    ]);
    $("quoteForm").hidden =
      !owner() ||
      selectedOrder.deliveryOption !== "delivery" ||
      selectedOrder.paymentStatus !== "PENDING";
    $("assignForm").hidden = !owner();
    $("fulfillOrder").hidden =
      selectedOrder.paymentStatus !== "PAID" ||
      selectedOrder.fulfillmentStatus !== "unfulfilled";
    quoteCountdown(selectedOrder.quote);
    message("");
    $("orderDialog").showModal();
    guard();
  }
  async function inspectException(id) {
    const data = await api(
      `/admin/payments/exceptions/${encodeURIComponent(id)}`,
    );
    if (selectedException?.id !== id) $("resolveForm").reset();
    selectedException = data;
    details("exceptionDetails", [
      ["Exception", data.exceptionType],
      ["Order", data.orderId || "Unmapped transaction"],
      ["Receipt", data.receiptNumber],
      ["Received", money(data.receivedAmountKes)],
      ["Expected", money(data.expectedAmountKes)],
      ["Status", data.status],
      ["Resolution", data.resolutionNotes || "—"],
      ["Evidence", data.evidence || "—"],
    ]);
    $("resolveForm").hidden = data.status !== "open";
    message("");
    $("exceptionDialog").showModal();
    guard();
  }
  async function mutate(path, body, success, after) {
    if (busy || connection.offline || !context.user) return;
    busy = true;
    guard();
    try {
      const result = await api(path, {
        method: "POST",
        body: JSON.stringify(body),
      });
      message(success, "success");
      if (after) await after(result);
    } catch (error) {
      message(error.message);
    } finally {
      busy = false;
      guard();
    }
  }
  function form(id, submit) {
    $(id).addEventListener("submit", (event) => {
      event.preventDefault();
      if (busy || connection.offline) return;
      const data = Object.fromEntries(new FormData(event.currentTarget));
      Promise.resolve(submit(data)).catch((error) => message(error.message));
    });
  }
  function sectionFromPath() {
    const last = window.location.pathname.split("/").filter(Boolean).pop();
    return ["inventory", "payments", "users"].includes(last)
      ? last
      : "orders";
  }
  function showSection(next) {
    if (
      (!owner() && ["payments", "users"].includes(next)) ||
      (!canManageInventory() && next === "inventory")
    ) {
      message("Your role does not permit this section.");
      next = "orders";
    }
    section = next;
    document.querySelectorAll("[data-panel]").forEach((panel) => {
      panel.hidden = panel.dataset.panel !== section;
    });
    document.querySelectorAll("[data-section]").forEach((link) => {
      if (link.dataset.section === section)
        link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    });
  }
  async function initialize() {
    try {
      const data = await api("/auth/me");
      if (!["owner", "employee"].includes(data.user?.role)) {
        endSession(
          "This account has no staff access. Contact the owner if you need access.",
        );
        return;
      }
      context.user = data.user;
      context.csrfToken = data.csrf_token;
      $("staffName").textContent = data.user.name || data.user.email;
      $("staffEmail").textContent = data.user.email;
      $("staffRole").textContent = data.user.role;
      $("adminSignIn").hidden = true;
      $("adminWorkspace").hidden = false;
      $("adminLogout").hidden = false;
      $("orderScope").textContent = owner()
        ? "All customer orders. Dispatch requires verified payment."
        : "Only orders assigned to you by the owner are shown.";
      document.querySelectorAll("[data-owner]").forEach((node) => {
        node.hidden = !owner();
      });
      document.querySelectorAll("[data-inventory]").forEach((node) => {
        node.hidden = !canManageInventory();
      });
      $("quoteForm").hidden = true;
      $("assignForm").hidden = true;
      try {
        const staged = JSON.parse(localStorage.getItem("shopSales") || "[]");
        $("metricStaged").textContent = Array.isArray(staged)
          ? staged.length
          : 0;
      } catch {
        $("metricStaged").textContent = 0;
      }
      guard();
      showSection(sectionFromPath());
      await Promise.all([
        loadOrders(),
        ...(canManageInventory() ? [loadInventory()] : []),
        ...(!["orders", "inventory"].includes(section) ? [refresh()] : []),
      ]);
    } catch (error) {
      message(error.message);
      if (!context.user) $("adminSignIn").hidden = false;
    }
  }
  document.querySelectorAll(".admin-dialog").forEach((dialog) => {
    const banner = el("p");
    banner.dataset.dialogMessage = "";
    banner.className = "admin-message";
    banner.setAttribute("role", "status");
    banner.hidden = true;
    dialog.prepend(banner);
  });
  document
    .querySelectorAll("[data-close-dialog]")
    .forEach((button) =>
      button.addEventListener("click", () => button.closest("dialog").close()),
    );
  document
    .querySelectorAll("[data-refresh]")
    .forEach((button) =>
      button.addEventListener("click", () =>
        loaders[button.dataset.refresh]().catch((error) =>
          message(error.message),
        ),
      ),
    );
  for (const direction of ["prev", "next"])
    document.querySelectorAll(`[data-page-${direction}]`).forEach((button) =>
      button.addEventListener("click", async () => {
        const name =
          button.dataset[direction === "prev" ? "pagePrev" : "pageNext"];
        const state = (pages[name] ||= { offset: 0 }),
          previous = state.offset;
        state.offset = Math.max(
          0,
          state.offset + (direction === "prev" ? -pageSize : pageSize),
        );
        try {
          await loaders[name]();
        } catch (error) {
          state.offset = previous;
          message(error.message);
        }
      }),
    );
  $("exceptionFilter").addEventListener("change", () => {
    pages.exceptions = { offset: 0 };
    loadExceptions().catch((error) => message(error.message));
  });
  $("adminLogout").addEventListener("click", () =>
    mutate("/auth/logout", {}, "Signed out.", async () => {
      context.user = null;
      context.csrfToken = "";
      localStorage.clear();
      sessionStorage.clear();
      window.location.assign("/");
    }),
  );
  $("invalidateCatalog").addEventListener("click", () =>
    mutate(
      "/catalog/invalidate",
      {},
      "Catalog cache invalidated.",
      loadInventory,
    ),
  );
  $("btnAddInventory")?.addEventListener("click", async () => {
    let prods = inventoryProducts;
    if (!prods.length) {
      try {
        const data = await api(`/operations/inventory?limit=100&offset=0`);
        prods = data.products || [];
        inventoryProducts = prods;
      } catch (error) {
        message(error.message);
        return;
      }
    }
    const select = $("restockProduct");
    select.replaceChildren();
    for (const p of prods) {
      const opt = el("option", `${p.name} (Current: ${p.stock})`);
      opt.value = p.id;
      select.append(opt);
    }
    $("addInventoryForm").reset();
    $("addInventoryForm").dataset.batchId =
      "restock_" + Date.now() + "_" + Math.random().toString(36).slice(2);
    $("addInventoryStatus").textContent = "";
    message("");
    $("addInventoryDialog").showModal();
    guard();
  });
  $("runReconciliation").addEventListener("click", () =>
    mutate(
      "/admin/payments/reconcile",
      {},
      "Reconciliation completed.",
      loaders.payments,
    ),
  );
  $("openSettlement").addEventListener("click", () => {
    message("");
    $("settlementDialog").showModal();
    guard();
  });
  $("fulfillOrder").addEventListener("click", () =>
    mutate(
      `/orders/${encodeURIComponent(selectedOrder.id)}/fulfill`,
      {},
      "Order marked dispatched / collected.",
      async () => {
        $("orderDialog").close();
        await loadOrders();
      },
    ),
  );
  form("quoteForm", (data) =>
    mutate(
      `/orders/${encodeURIComponent(selectedOrder.id)}/delivery-quote`,
      { fee_kes: Number(data.fee_kes), notes: data.notes },
      "Delivery quote offered to the customer.",
      async (result) => {
        selectedOrder.quote = result.quote;
        quoteCountdown(result.quote);
        await loadOrders();
      },
    ),
  );
  form("assignForm", (data) =>
    mutate(
      `/orders/${encodeURIComponent(selectedOrder.id)}/assign`,
      data,
      "Order assigned.",
      loadOrders,
    ),
  );
  form("resolveForm", (data) =>
    mutate(
      `/admin/payments/exceptions/${encodeURIComponent(selectedException.id)}/resolve`,
      data,
      "Exception resolution recorded.",
      async () => {
        $("exceptionDialog").close();
        $("resolveForm").reset();
        await loaders.payments();
      },
    ),
  );
  form("settlementForm", (data) =>
    mutate(
      "/admin/payments/manual-settlement",
      { ...data, amount_kes: Number(data.amount_kes) },
      "Verified settlement recorded.",
      async () => {
        $("settlementDialog").close();
        $("settlementForm").reset();
        await loaders.payments();
        await loadOrders();
      },
    ),
  );
  form("addInventoryForm", (data) => {
    const batchId =
      $("addInventoryForm").dataset.batchId ||
      "restock_" + Date.now() + "_" + Math.random().toString(36).slice(2);
    return mutate(
      "/operations/inventory/restock",
      {
        batch_id: batchId,
        product_id: data.product_id,
        quantity: Number(data.quantity),
        reference: data.reference,
      },
      "Inventory restocked successfully.",
      async () => {
        delete $("addInventoryForm").dataset.batchId;
        $("addInventoryDialog").close();
        $("addInventoryForm").reset();
        await loadInventory();
      },
    );
  });
  window.addEventListener("bliss:connection", () => {
    guard();
    if (!connection.offline) {
      if (!context.user) initialize();
      else {
        // User-table role selections are drafts too. Refresh operational reads
        // without replacing those controls during connection recovery.
        const reads = [loadOrders(), loadInventory()];
        if (section === "payments") reads.push(loaders.payments());
        Promise.all(reads).catch((error) => message(error.message));
      }
    }
  });
  setInterval(() => {
    if (selectedOrder) quoteCountdown(selectedOrder.quote);
  }, 1000);
  setInterval(() => {
    if (
      context.user &&
      !connection.offline &&
      document.visibilityState !== "hidden" &&
      !busy
    )
      Promise.all([loadOrders(), loadInventory()]).catch((error) =>
        message(error.message),
      );
  }, 15000);
  initialize();
})();
