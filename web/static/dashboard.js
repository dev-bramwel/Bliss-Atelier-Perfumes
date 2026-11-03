// --------------------
// ATELIER DASHBOARD
// --------------------

(function () {
  // Elements
  const tabButtons = document.querySelectorAll(".dash-tab-btn");
  const tabContents = document.querySelectorAll(".dash-tab-content");

  const fastSaleForm = document.getElementById("fastSaleForm");
  const saleFragranceSelect = document.getElementById("saleFragranceSelect");
  const saleQuantity = document.getElementById("saleQuantity");
  const saleOperator = document.getElementById("saleOperator");
  const salePaymentType = document.getElementById("salePaymentType");
  const saleReceiptRef = document.getElementById("saleReceiptRef");
  const saleCustomerPhone = document.getElementById("saleCustomerPhone");
  const saleNotes = document.getElementById("saleNotes");
  const saleCalculatedTotal = document.getElementById("saleCalculatedTotal");

  const recentSalesTbody = document.getElementById("recentSalesTbody");
  const clearSalesHistoryBtn = document.getElementById("clearSalesHistoryBtn");
  const ordersQueueList = document.getElementById("ordersQueueList");
  const queueFilterBtns = document.querySelectorAll(".queue-filter-btn");

  const dashSalesCount = document.getElementById("dashSalesCount");
  const dashSalesTotal = document.getElementById("dashSalesTotal");
  const dashOrdersCount = document.getElementById("dashOrdersCount");
  const dashCatalogSearch = document.getElementById("dashCatalogSearch");
  const dashCatalogGrid = document.getElementById("dashCatalogGrid");

  let activeFilter = "all";

  // --------------------
  // LOCAL STORAGE FOR SHOP SALES
  // --------------------
  function loadShopSales() {
    try {
      const parsed = JSON.parse(localStorage.getItem("shopSales") || "[]");
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      return [];
    }
  }

  function saveShopSales(sales) {
    localStorage.setItem("shopSales", JSON.stringify(Array.isArray(sales) ? sales : []));
  }

  // --------------------
  // TAB NAVIGATION
  // --------------------
  tabButtons.forEach((btn) => {
    btn.addEventListener("click", () => {
      const target = btn.getAttribute("data-tab-target");
      if (!target) return;

      tabButtons.forEach((b) => b.classList.remove("active"));
      tabContents.forEach((c) => c.classList.add("hidden"));

      btn.classList.add("active");
      const activeContent = document.getElementById(`tab-${target}`);
      if (activeContent) {
        activeContent.classList.remove("hidden");
      }
    });
  });

  // --------------------
  // CALCULATED PRICE
  // --------------------
  function updateCalculatedTotal() {
    if (!saleFragranceSelect || !saleQuantity || !saleCalculatedTotal) return;
    const selectedOption =
      saleFragranceSelect.options?.[saleFragranceSelect.selectedIndex];
    const unitPrice = selectedOption
      ? parseInt(selectedOption.getAttribute?.("data-price") || "2500", 10)
      : 2500;
    const qty = Math.max(1, parseInt(saleQuantity.value || "1", 10));
    saleCalculatedTotal.textContent = formatKes(unitPrice * qty);
  }

  saleFragranceSelect?.addEventListener("change", updateCalculatedTotal);
  saleQuantity?.addEventListener("input", updateCalculatedTotal);

  // --------------------
  // FAST SALE RECORDING
  // --------------------
  fastSaleForm?.addEventListener("submit", (e) => {
    e.preventDefault();

    const fragranceId = saleFragranceSelect?.value;
    if (!fragranceId) {
      alert("Please select a fragrance.");
      return;
    }

    const selectedOption =
      saleFragranceSelect.options?.[saleFragranceSelect.selectedIndex];
    const unitPrice = selectedOption
      ? parseInt(selectedOption.getAttribute?.("data-price") || "2500", 10)
      : 2500;
    const qty = Math.max(1, parseInt(saleQuantity.value || "1", 10));
    const totalKes = unitPrice * qty;

    const p = getProduct(fragranceId);
    const fragranceName = p ? p.name : fragranceId;

    const stockBatch = {
      id: "sale-" + Date.now(),
      kind: "local_sale",
      actor: saleOperator?.value || "operator-1",
      evidence: (saleReceiptRef?.value || "").trim() || ("verified-sale-" + Date.now()),
      lines: [{ productId: fragranceId, quantity: qty }],
    };

    const newSale = {
      id: "sale-" + Date.now(),
      fragranceId,
      fragranceName,
      qty,
      unitPrice,
      totalKes,
      operator: saleOperator?.value || "Employee 1",
      paymentType: salePaymentType?.value || "mpesa_stk",
      receiptRef: (saleReceiptRef?.value || "").trim(),
      customerPhone: (saleCustomerPhone?.value || "").trim(),
      notes: (saleNotes?.value || "").trim(),
      recordedAt: new Date().toISOString(),
      status: "VERIFIED_FOR_REVIEW",
      stockBatch,
    };

    // Deduct verified sale against authoritative stock ledger if endpoint is active
    try {
      fetch("/api/stock/apply", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(stockBatch),
        signal: AbortSignal.timeout(2000),
      }).catch(() => {});
    } catch {}

    const sales = loadShopSales();
    sales.unshift(newSale);
    saveShopSales(sales);

    // Reset fields except operator
    if (saleFragranceSelect) saleFragranceSelect.selectedIndex = 0;
    if (saleQuantity) saleQuantity.value = "1";
    if (saleReceiptRef) saleReceiptRef.value = "";
    if (saleCustomerPhone) saleCustomerPhone.value = "";
    if (saleNotes) saleNotes.value = "";
    updateCalculatedTotal();

    renderDashboardData();

    // Show temporary toast
    showToast(`Staged batch for ${fragranceName} ×${qty} (KES ${formatKes(totalKes)}) - Ready for stock apply`);
  });

  clearSalesHistoryBtn?.addEventListener("click", () => {
    if (confirm("Clear local counter sales history?")) {
      saveShopSales([]);
      renderDashboardData();
    }
  });

  recentSalesTbody?.addEventListener("click", (e) => {
    const btn = e.target.closest(".copy-batch-btn");
    if (!btn) return;
    const jsonStr = btn.getAttribute("data-batch");
    if (!jsonStr) return;
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        navigator.clipboard.writeText(jsonStr);
      }
      showToast("Copied 'stock apply' batch JSON to clipboard");
    } catch {
      showToast("Batch JSON staged for stock apply");
    }
  });

  // --------------------
  // TOAST FEEDBACK
  // --------------------
  function showToast(msg) {
    let toast = document.getElementById("dashToast");
    if (!toast) {
      toast = document.createElement("div");
      toast.id = "dashToast";
      toast.className =
        "fixed bottom-6 right-6 z-50 bg-[#1a1512] text-[#faf7f2] border border-[#c6b79b]/40 px-5 py-3 rounded-2xl shadow-2xl text-xs font-semibold backdrop-blur-xl transition-all duration-300 opacity-0 translate-y-3 pointer-events-none";
      document.body.appendChild(toast);
    }
    toast.textContent = msg;
    toast.style.opacity = "1";
    toast.style.transform = "translateY(0)";
    setTimeout(() => {
      toast.style.opacity = "0";
      toast.style.transform = "translateY(10px)";
    }, 3500);
  }

  // --------------------
  // RENDER RECENT COUNTER SALES
  // --------------------
  function renderRecentSales(sales) {
    if (!recentSalesTbody) return;

    if (!sales || sales.length === 0) {
      recentSalesTbody.innerHTML = `
        <tr>
          <td colspan="7" class="py-6 text-center text-gray-500">
            No counter sales recorded yet today. Use the form above to record a verified sale.
          </td>
        </tr>`;
      return;
    }

    recentSalesTbody.innerHTML = sales
      .slice(0, 15)
      .map((s) => {
        const time = new Date(s.recordedAt).toLocaleTimeString([], {
          hour: "2-digit",
          minute: "2-digit",
        });
        const paymentLabel =
          s.paymentType === "mpesa_stk"
            ? "M-Pesa STK"
            : s.paymentType === "mpesa_counter"
              ? "Till/Paybill"
              : "Cash";

        return `
          <tr class="hover:bg-white/[0.02] transition">
            <td class="py-2.5 px-3 text-gray-400 font-mono">${escapeHTML(time)}</td>
            <td class="py-2.5 px-3 font-medium text-white">${escapeHTML(s.fragranceName)}</td>
            <td class="py-2.5 px-3 text-gray-300">${s.qty}</td>
            <td class="py-2.5 px-3 text-[var(--gold-soft)] font-semibold">KES ${formatKes(s.totalKes)}</td>
            <td class="py-2.5 px-3 text-gray-400">${escapeHTML(s.operator)}</td>
            <td class="py-2.5 px-3 text-gray-400 font-mono">
              <span class="text-[10px] text-gray-500">${paymentLabel}:</span>
              ${escapeHTML(s.receiptRef || "–")}
            </td>
            <td class="py-2.5 px-3">
              <div class="flex items-center gap-1.5 flex-wrap">
                <span class="dash-badge dash-badge-pending text-[10px]">● Staged (local_sale)</span>
                <button
                  type="button"
                  class="copy-batch-btn text-[10px] text-gray-400 hover:text-[var(--gold-soft)] underline cursor-pointer"
                  data-batch="${escapeHTML(JSON.stringify(s.stockBatch || {}))}"
                  title="Copy canonical local_sale batch JSON for stock apply"
                >
                  Copy JSON
                </button>
              </div>
            </td>
          </tr>`;
      })
      .join("");
  }

  // --------------------
  // SERVER ORDERS SYNC
  // --------------------
  let serverOrders = null;
  async function syncServerOrders() {
    try {
      const res = await fetch("/api/orders", {
        cache: "no-store",
        signal: AbortSignal.timeout(3000),
      });
      if (res.ok) {
        const data = await res.json();
        const list = Array.isArray(data?.orders)
          ? data.orders
          : Array.isArray(data)
            ? data
            : null;
        if (list) {
          serverOrders = list;
          const badge = document.getElementById("orderSyncBadge");
          if (badge) {
            badge.textContent = "Server Sync Active (" + list.length + " orders)";
            badge.className = "dash-badge dash-badge-success text-[10px]";
          }
          renderDashboardData();
          return;
        }
      }
    } catch {}
    const badge = document.getElementById("orderSyncBadge");
    if (badge) {
      badge.textContent = "Device Session Queue (Standalone / Offline)";
      badge.className = "dash-badge dash-badge-khaki text-[10px]";
    }
  }

  // --------------------
  // RENDER ORDERS QUEUE
  // --------------------
  function renderOrdersQueue(sales) {
    if (!ordersQueueList) return;

    const sourceOrders = serverOrders !== null ? serverOrders : loadOrders();
    const onlineOrders = (Array.isArray(sourceOrders) ? sourceOrders : []).map((o) => ({
      ...o,
      _type: "online",
      _timestamp: new Date(o.createdAt || o._savedAt || Date.now()).getTime(),
    }));

    const counterSales = (Array.isArray(sales) ? sales : []).map((s) => ({
      ...s,
      _type: "counter",
      _timestamp: new Date(s.recordedAt || Date.now()).getTime(),
    }));

    let allItems = [...onlineOrders, ...counterSales].sort(
      (a, b) => b._timestamp - a._timestamp,
    );

    if (activeFilter === "online") {
      allItems = allItems.filter((i) => i._type === "online");
    } else if (activeFilter === "counter") {
      allItems = allItems.filter((i) => i._type === "counter");
    }

    if (allItems.length === 0) {
      ordersQueueList.innerHTML = `
        <div class="py-8 text-center text-gray-500 text-sm">
          No orders or transactions matching filter.
        </div>`;
      return;
    }

    ordersQueueList.innerHTML = allItems
      .slice(0, 20)
      .map((item) => {
        if (item._type === "online") {
          const dateStr = new Date(item._timestamp).toLocaleString([], {
            month: "short",
            day: "numeric",
            hour: "2-digit",
            minute: "2-digit",
          });
          const customerName = item.customerName || item.customer?.name || "Guest";
          const customerPhone = item.customerPhone || item.customer?.phone || "–";
          const location = item.deliveryLocation || item.customer?.location || "Mombasa";
          const deliveryOption = item.deliveryOption || item.customer?.delivery || "delivery";
          const total = item.totalKes || 0;
          const status = item.paymentStatus || "PENDING";
          const isPaid = status === "PAID";

          const itemsSummary = Array.isArray(item.items)
            ? item.items.map((i) => `${i.name || i.id} ×${i.qty}`).join(", ")
            : "Perfume order";

          return `
            <div class="p-4 rounded-2xl bg-[rgba(26,21,18,0.7)] border border-white/10 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
              <div class="space-y-1 min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="dash-badge dash-badge-khaki text-[10px]">Online Checkout</span>
                  <span class="text-xs text-gray-400 font-mono">${escapeHTML(dateStr)}</span>
                  <span class="dash-badge ${isPaid ? "dash-badge-success" : "dash-badge-pending"} text-[10px]">
                    ${isPaid ? "PAID" : "PENDING VERIFICATION"}
                  </span>
                </div>
                <div class="text-sm font-semibold text-[#faf7f2]">
                  ${escapeHTML(customerName)} &bull; <span class="text-gray-400 font-normal font-mono">${escapeHTML(customerPhone)}</span>
                </div>
                <div class="text-xs text-gray-400">
                  <span class="text-[var(--gold-soft)]">${escapeHTML(itemsSummary)}</span>
                  &bull; ${deliveryOption === "pickup" ? "Showroom Pickup" : "Delivery: " + escapeHTML(location)}
                </div>
              </div>
              <div class="text-right shrink-0">
                <div class="text-base font-bold text-[var(--gold-soft)]">KES ${formatKes(total)}</div>
                <div class="text-[10px] text-gray-500 uppercase tracking-wider">Payment Pre-Dispatch</div>
              </div>
            </div>`;
        } else {
          // Counter Sale
          const dateStr = new Date(item._timestamp).toLocaleString([], {
            month: "short",
            day: "numeric",
            hour: "2-digit",
            minute: "2-digit",
          });

          return `
            <div class="p-4 rounded-2xl bg-[rgba(26,21,18,0.7)] border border-white/10 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
              <div class="space-y-1 min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="dash-badge dash-badge-espresso text-[10px]">Shop Counter</span>
                  <span class="text-xs text-gray-400 font-mono">${escapeHTML(dateStr)}</span>
                  <span class="dash-badge dash-badge-khaki text-[10px]">● Verified Sale</span>
                </div>
                <div class="text-sm font-semibold text-[#faf7f2]">
                  ${escapeHTML(item.fragranceName)} ×${item.qty} &bull; <span class="text-gray-400 font-normal">Operator: ${escapeHTML(item.operator)}</span>
                </div>
                <div class="text-xs text-gray-400 font-mono">
                  Ref: ${escapeHTML(item.receiptRef || "–")} ${item.notes ? "• " + escapeHTML(item.notes) : ""}
                </div>
              </div>
              <div class="text-right shrink-0">
                <div class="text-base font-bold text-[var(--gold-soft)]">KES ${formatKes(item.totalKes)}</div>
                <div class="text-[10px] text-gray-500 uppercase tracking-wider">Verified at Counter</div>
              </div>
            </div>`;
        }
      })
      .join("");
  }

  // --------------------
  // FILTER QUEUE BUTTONS
  // --------------------
  queueFilterBtns.forEach((btn) => {
    btn.addEventListener("click", () => {
      queueFilterBtns.forEach((b) => {
        b.classList.remove("bg-[#c6b79b]", "text-[#120f0d]", "font-semibold");
        b.classList.add("text-gray-300");
      });
      btn.classList.add("bg-[#c6b79b]", "text-[#120f0d]", "font-semibold");
      btn.classList.remove("text-gray-300");

      activeFilter = btn.getAttribute("data-filter") || "all";
      renderOrdersQueue(loadShopSales());
    });
  });

  // --------------------
  // CATALOG SEARCH FILTER IN TAB 3
  // --------------------
  dashCatalogSearch?.addEventListener("input", (e) => {
    const q = (e.target.value || "").toLowerCase().trim();
    const cards = dashCatalogGrid?.querySelectorAll(".dash-metric-card");
    if (!cards) return;

    cards.forEach((card) => {
      const name = (card.getAttribute("data-name") || "").toLowerCase();
      const cat = (card.getAttribute("data-category") || "").toLowerCase();
      const match = !q || name.includes(q) || cat.includes(q);
      card.style.display = match ? "flex" : "none";
    });
  });

  // --------------------
  // RENDER ALL DATA & METRICS
  // --------------------
  function renderDashboardData() {
    const sales = loadShopSales();
    const rawOrders = serverOrders !== null ? serverOrders : loadOrders();
    const ordersList = Array.isArray(rawOrders) ? rawOrders : [];

    // Metrics
    if (dashSalesCount) dashSalesCount.textContent = String(sales.length);
    const totalSalesKes = sales.reduce((acc, s) => acc + (s.totalKes || 0), 0);
    if (dashSalesTotal) dashSalesTotal.textContent = formatKes(totalSalesKes);

    if (dashOrdersCount) dashOrdersCount.textContent = String(ordersList.length);

    renderRecentSales(sales);
    renderOrdersQueue(sales);
  }

  // Initial load
  updateCalculatedTotal();
  renderDashboardData();
  syncServerOrders();

  document.addEventListener("catalogUpdated", renderDashboardData);
  setInterval(syncServerOrders, 30000);
})();
