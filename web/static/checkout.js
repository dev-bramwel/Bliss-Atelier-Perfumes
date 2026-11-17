(() => {
  const shop = window.BlissShopper;
  const $ = (id) => document.getElementById(id);
  const form = $("checkoutForm");
  if (!form || !shop) return;
  let active = null,
    order = null,
    status = null,
    quote = null,
    breakdown = null;
  let busy = false,
    polling = false,
    timer = null,
    generation = 0,
    blocked = false;
  let retry = null;
  const pathnameID = /^\/order\/([^/]+)$/.exec(location.pathname)?.[1];
  const message = (text) => {
    $("checkoutMessage").textContent = text;
    $("checkoutMessage").hidden = !text;
  };
  const money = (value) => "KES " + Number(value || 0).toLocaleString("en-KE");
  const tokenHeaders = () =>
    active?.token ? { "X-Guest-Token": active.token } : {};
  const path = (suffix) =>
    "/orders/" + encodeURIComponent(active.id) + (suffix || "");
  const expiry = () =>
    status?.expiresAt ||
    order?.hold_expires_at ||
    order?.reservation?.expiresAt;
  const expired = () =>
    status?.isExpired || (expiry() && Date.parse(expiry()) <= shop.now);
  const paid = () =>
    (status?.paymentStatus || order?.payment_status || order?.paymentStatus) ===
    "PAID";
  const delivery = () =>
    (order?.delivery_option || order?.deliveryOption) !== "pickup" &&
    (order?.delivery_option || order?.deliveryOption) !== "showroom_pickup";
  const attempt = () =>
    status?.latestAttempt || order?.latest_attempt || order?.latestAttempt;
  const ambiguous = () =>
    ["created", "initiating", "accepted", "unknown"].includes(
      attempt()?.status,
    );
  const quoteOffered = () =>
    quote &&
    ["pending", "offered"].includes(quote.status) &&
    Date.parse(quote.expires_at) > shop.now;
  const payable = () => !delivery() || quote?.status === "accepted";
  function countdown(value) {
    const seconds = Math.max(
      0,
      Math.ceil((Date.parse(value) - shop.now) / 1000),
    );
    return (
      Math.floor(seconds / 60)
        .toString()
        .padStart(2, "0") +
      ":" +
      (seconds % 60).toString().padStart(2, "0")
    );
  }
  function syncControls() {
    form.noValidate = !!shop.read("bliss_pending_order");
    const locked = busy || blocked || window.BlissConnection.offline;
    $("placeOrder").disabled =
      locked ||
      (!shop.read("bliss_pending_order") &&
        (!cartCount() || !cartAvailable())) ||
      !!active;
    $("acceptQuote").disabled = $("rejectQuote").disabled =
      locked || expired() || !quoteOffered();
    $("payOrder").disabled =
      locked || expired() || !payable() || ambiguous() || paid();
    $("switchPickup").disabled = locked || ambiguous() || paid() || expired();
    if (window.BlissConnection.offline) window.BlissConnection.protectButtons();
  }
  function tick() {
    if (!active) return;
    const hold = expiry();
    $("holdTimer").textContent = paid()
      ? "Payment received - reservation completed"
      : hold
        ? countdown(hold) + " Reservation Hold Remaining"
        : "Checking reservation time...";
    $("holdExpired").hidden = !expired() || paid();
    $("restartOrder").hidden = !expired() || paid() || ambiguous();
    $("quoteTimer").textContent = quoteOffered()
      ? "Quote expires in " + countdown(quote.expires_at)
      : "";
    syncControls();
  }
  function renderSummary() {
    if (active) return;
    $("checkoutTotal").textContent =
      catalogState === "ready" ? formatKes(cartTotal()) : "...";
    $("checkoutItems").innerHTML =
      Object.entries(cart)
        .map(([id, qty]) => {
          const product = getProduct(id);
          return (
            '<div class="shopper-line"><span>' +
            escapeHTML(product?.name || id) +
            " - " +
            Number(qty) +
            "</span><strong>" +
            (product ? money(product.priceKes * qty) : "Unavailable") +
            "</strong></div>"
          );
        })
        .join("") ||
      '<p>Your cart is empty. <a href="/">Explore the collection</a></p>';
    syncControls();
  }
  function renderOrder() {
    if (!order) return;
    $("checkoutWorkspace").hidden = true;
    $("orderJourney").hidden = false;
    $("journeyOrderID").textContent = active.id;
    $("journeyTitle").textContent = paid()
      ? "Your perfume is confirmed"
      : "Your order is saved";
    $("savedItems").innerHTML = (order.items || [])
      .map(
        (item) =>
          '<div class="shopper-line"><span>' +
          escapeHTML(item.product_name || item.productName || item.name || "") +
          " - " +
          Number(item.quantity || item.qty) +
          "</span><strong>" +
          money(item.subtotal_kes ?? item.subtotalKes) +
          "</strong></div>",
      )
      .join("");
    const total =
      breakdown?.payable_merchant_kes ?? order.total_kes ?? order.totalKes;
    $("payableTotal").textContent =
      delivery() && !payable()
        ? "Items subtotal: " +
          money(order.total_kes ?? order.totalKes) +
          " - awaiting transport quote"
        : "Payable to the atelier: " + money(total);
    $("courierTotal").textContent = breakdown?.customer_direct_kes
      ? "Pay the courier separately: " + money(breakdown.customer_direct_kes)
      : "";
    $("quoteCard").hidden = !delivery();
    const offered = quoteOffered();
    $("quoteState").textContent = !quote
      ? "Delivery quote being calculated by atelier staff..."
      : quote.status === "accepted"
        ? "Transport quote accepted"
        : quote.status === "rejected"
          ? "You declined this quote. Request a new quote or choose free pickup."
          : offered
            ? "Your transport quote is ready"
            : "This quote expired. Waiting for a new quote.";
    $("quoteFee").textContent = quote
      ? "Transport fee: " +
        money(quote.amount_kes ?? quote.fee_kes) +
        (quote.fee_recipient === "courier"
          ? " - paid directly to the courier"
          : " - included in your M-Pesa payment")
      : "";
    $("quoteNotes").textContent =
      quote?.arrangement || quote?.notes || quote?.customer_notes || "";
    $("acceptQuote").hidden = $("rejectQuote").hidden = !offered || paid();
    $("quoteReason").hidden = $("quoteReasonLabel").hidden = !offered || paid();
    $("pickupSuggestion").hidden = $("switchPickup").hidden =
      !delivery() || paid();
    const current =
      status?.paymentStatus || order.payment_status || order.paymentStatus;
    const latest = attempt();
    $("paymentReceipt").textContent = "";
    if (paid()) {
      $("paymentTitle").textContent = "Payment confirmed";
      $("paymentMessage").textContent =
        "Thank you. Your perfume is being prepared. Payment received: " +
        money(latest?.amountKes ?? total) +
        ".";
      $("paymentReceipt").textContent =
        "M-Pesa receipt: " +
        (status?.receiptNumber ||
          status?.MpesaReceiptNumber ||
          "Confirmation pending");
      if (!active.cartCleared) {
        // Remove only the purchased quantities; preserve items added while this order was open.
        for (const [id, qty] of Object.entries(active.items || {})) {
          const remaining = (cart[id] || 0) - qty;
          if (remaining > 0) cart[id] = remaining;
          else delete cart[id];
        }
        saveCart(cart);
        document.dispatchEvent(new Event("cartUpdated"));
        active.cartCleared = true;
        persist();
        localStorage.removeItem("bliss_active_order");
        shop.profile();
      }
      clearTimeout(timer);
    } else if (
      !ambiguous() &&
      (current === "FAILED" || latest?.status === "failed")
    ) {
      $("paymentTitle").textContent = "Payment unsuccessful";
      $("paymentMessage").textContent =
        latest?.errorMessage ||
        "The transaction failed. You can retry while your reservation is active.";
      clearTimeout(timer);
    } else if (!payable()) {
      $("paymentTitle").textContent = "Accept your delivery quote to pay";
      $("paymentMessage").textContent =
        "No M-Pesa prompt will be sent until you accept the transport quote.";
    } else if (ambiguous()) {
      $("paymentTitle").textContent =
        latest?.status === "unknown"
          ? "Payment confirmation pending"
          : "Check your phone for the M-Pesa prompt";
      $("paymentMessage").textContent = latest?.phone
        ? "Phone: " +
          latest.phone +
          ". We are checking this request. Do not pay again."
        : "We are checking the existing payment request. Do not pay again.";
    } else {
      $("paymentTitle").textContent = expired()
        ? "Reservation expired"
        : "Ready to pay";
      $("paymentMessage").textContent = expired()
        ? "Check this order before starting again. A late payment will be reviewed by the atelier."
        : "Send a secure M-Pesa prompt to your phone.";
    }
    $("payOrder").hidden = paid() || !payable() || ambiguous() || expired();
    $("payOrder").textContent = retry
      ? "Check and retry same payment request"
      : current === "FAILED" || latest?.status === "failed"
        ? "Retry Payment"
        : "Pay " + money(total) + " with M-Pesa";
    tick();
  }
  function persist() {
    shop.write("bliss_order_" + active.id, active);
    shop.write("bliss_active_order", active);
    shop.profile();
  }
  async function call(suffix, options) {
    return shop.request(path(suffix), {
      ...options,
      headers: { ...tokenHeaders(), ...options?.headers },
    });
  }
  async function refresh() {
    if (!active || polling || window.BlissConnection.offline) return;
    polling = true;
    const currentGeneration = generation;
    try {
      await shop.ready;
      if (active.authenticated && !shop.user)
        throw Object.assign(new Error("Sign in to resume your order."), {
          status: 401,
        });
      const [data, nextStatus] = await Promise.all([
        call(""),
        call("/payment-status"),
      ]);
      if (generation !== currentGeneration) return;
      order = data.order;
      status = nextStatus;
      if (retry && (ambiguous() || paid())) {
        retry = null;
        localStorage.removeItem("bliss_retry_" + active.id);
      }
      if (delivery()) {
        try {
          const quoteData = await call("/delivery-quote");
          quote = quoteData.quote;
          breakdown = quoteData.breakdown || order.payable_breakdown;
        } catch (error) {
          if (error.status === 404) {
            quote = null;
            breakdown = null;
          } else throw error;
        }
      } else {
        quote = null;
        breakdown = order.payable_breakdown;
      }
      blocked = false;
      renderOrder();
    } catch (error) {
      if (error.status === 401 || error.status === 403) blocked = true;
      message(error.message);
      syncControls();
    } finally {
      polling = false;
      clearTimeout(timer);
      if (
        generation === currentGeneration &&
        active &&
        !blocked &&
        !document.hidden &&
        !window.BlissConnection.offline &&
        !paid() &&
        !expired()
      ) {
        // Terminal failures stop payment polling; manual status checks and explicit retry remain available.
        const failed =
          !ambiguous() &&
          (status?.paymentStatus === "FAILED" ||
            attempt()?.status === "failed");
        if (!failed || (delivery() && !payable()))
          timer = setTimeout(refresh, 2500);
      }
    }
  }
  async function mutate(action) {
    if (busy || blocked || window.BlissConnection.offline) return;
    busy = true;
    syncControls();
    message("");
    try {
      await action();
    } catch (error) {
      message(error.message);
    } finally {
      busy = false;
      $("placeOrder").textContent = shop.read("bliss_pending_order")
        ? "Recover saved order request"
        : "Place Order";
      syncControls();
    }
  }
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    if (
      active ||
      busy ||
      (!shop.read("bliss_pending_order") && !form.reportValidity())
    )
      return;
    mutate(async () => {
      await shop.ready;
      const previous = shop.read("bliss_pending_order");
      if (previous && previous.actor !== (shop.user?.id || "guest"))
        throw new Error(
          "Sign in with the same account to recover your pending order.",
        );
      const fd = new FormData(form);
      let phone = String(fd.get("phone")).replace(/[\s+-]/g, "");
      if (/^07\d{8}$/.test(phone)) phone = "254" + phone.slice(1);
      if (!previous && !/^2547\d{8}$/.test(phone))
        throw new Error(
          "Enter a valid 07XXXXXXXX or 2547XXXXXXXX M-Pesa phone.",
        );
      const payload = {
        customer: {
          name: String(fd.get("name")).trim(),
          phone,
          delivery: String(fd.get("delivery")),
          location:
            fd.get("delivery") === "pickup"
              ? "Mombasa Atelier"
              : String(fd.get("location")).trim(),
          notes: String(fd.get("notes") || "").trim(),
          payment: "pay_now",
        },
        items: Object.entries(cart).map(([id, qty]) => ({ id, qty })),
      };
      const body = previous?.body || JSON.stringify(payload),
        actor = shop.user?.id || "guest";
      let pending = shop.read("bliss_pending_order");
      if (pending && pending.actor !== actor)
        throw new Error(
          "An earlier order request is unconfirmed. Restore the same cart and details, then retry it to recover your order.",
        );
      if (!pending) {
        await loadCatalog();
        if (!cartCount() || !cartAvailable())
          throw new Error(
            "Some products are unavailable. Please review your cart.",
          );
        pending = { key: shop.uuid(), body, actor, items: { ...cart } };
        shop.write("bliss_pending_order", pending);
      }
      let data;
      try {
        let checkoutToken = localStorage.getItem("bliss_checkout_token");
        if (!/^ct_[a-f0-9]{64}$/.test(checkoutToken || "")) {
          checkoutToken =
            "ct_" +
            Array.from(crypto.getRandomValues(new Uint8Array(32)), (value) =>
              value.toString(16).padStart(2, "0"),
            ).join("");
          localStorage.setItem("bliss_checkout_token", checkoutToken);
        }
        data = await shop.request("/orders", {
          method: "POST",
          headers: {
            "Idempotency-Key": pending.key,
            "X-Checkout-Token": checkoutToken,
          },
          body: pending.body,
        });
      } catch (error) {
        // A received validation response is definitive; transport/5xx/in-progress outcomes retain the key and exact body.
        if ([400, 404, 422].includes(error.status))
          localStorage.removeItem("bliss_pending_order");
        throw error;
      }
      if (!data.order?.id || (!shop.user && !data.guest_token))
        throw new Error(
          "Order response incomplete. Retry the same request to recover access.",
        );
      active = {
        id: data.order.id,
        token: shop.user ? "" : data.guest_token,
        authenticated: !!shop.user,
        items: pending.items,
        cartCleared: false,
      };
      if (active.token) localStorage.setItem("bliss_guest_token", active.token);
      order = data.order;
      status = null;
      quote = null;
      persist();
      localStorage.removeItem("bliss_pending_order");
      history.replaceState(null, "", "/order/" + encodeURIComponent(active.id));
      message(
        data.warning ||
          "Your order is saved. Keep this browser to resume securely.",
      );
      renderOrder();
      await refresh();
    });
  });
  $("acceptQuote").addEventListener("click", () =>
    mutate(async () => {
      if (!quoteOffered() || expired()) return;
      await call("/delivery-quote/accept", {
        method: "POST",
        body: JSON.stringify({ version: quote.version }),
      });
      await refresh();
    }),
  );
  $("rejectQuote").addEventListener("click", () =>
    mutate(async () => {
      if (!quoteOffered() || expired()) return;
      await call("/delivery-quote/reject", {
        method: "POST",
        body: JSON.stringify({
          version: quote.version,
          reason: $("quoteReason").value,
        }),
      });
      await refresh();
    }),
  );
  $("switchPickup").addEventListener("click", () =>
    mutate(async () => {
      await call("/pickup", { method: "POST", body: "{}" });
      await refresh();
    }),
  );
  $("payOrder").addEventListener("click", () =>
    mutate(async () => {
      await refresh();
      if (blocked || paid() || expired() || !payable() || ambiguous()) return;
      const key =
        retry || shop.read("bliss_retry_" + active.id)?.key || shop.uuid();
      retry = key;
      shop.write("bliss_retry_" + active.id, { key });
      try {
        const data = await call("/retry-payment", {
          method: "POST",
          headers: { "Idempotency-Key": key },
          body: "{}",
        });
        status = {
          ...status,
          paymentStatus: "PENDING",
          latestAttempt: data.attempt,
        };
        retry = null;
        localStorage.removeItem("bliss_retry_" + active.id);
        message(data.warning || "Check your phone for the M-Pesa prompt.");
      } catch (error) {
        if ([400, 422].includes(error.status)) {
          retry = null;
          localStorage.removeItem("bliss_retry_" + active.id);
        }
        throw error;
      } finally {
        await refresh();
      }
    }),
  );
  $("refreshOrder").addEventListener("click", () => refresh());
  $("refreshAttempts").addEventListener("click", () =>
    mutate(async () => {
      const data = await call("/attempts");
      $("attemptsHistory").innerHTML =
        (data.attempts || [])
          .map(
            (a) =>
              "<li>Attempt " +
              Number(a.attemptNumber) +
              " - " +
              escapeHTML(a.status) +
              " - " +
              money(a.amountKes) +
              " - " +
              escapeHTML(new Date(a.createdAt).toLocaleString("en-KE")) +
              (a.errorMessage ? " - " + escapeHTML(a.errorMessage) : "") +
              "</li>",
          )
          .join("") || "<li>No payment attempts yet.</li>";
    }),
  );
  $("restartOrder").addEventListener("click", () => {
    if (!expired() || ambiguous() || paid()) return;
    localStorage.removeItem("bliss_active_order");
    location.href = "/cart";
  });
  function deliveryFields() {
    const isDelivery = form.elements.delivery.value === "delivery";
    $("deliveryAddress").hidden = !isDelivery;
    $("customerLocation").required = isDelivery;
  }
  form.addEventListener("change", deliveryFields);
  deliveryFields();
  window.addEventListener("bliss:connection", () => {
    syncControls();
    if (!window.BlissConnection.offline) {
      message("Connection restored. Checking your existing order...");
      refresh();
    } else clearTimeout(timer);
  });
  window.addEventListener("bliss:identity", () => {
    blocked = false;
    refresh();
  });
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refresh();
    else clearTimeout(timer);
  });
  document.addEventListener("catalogUpdated", renderSummary);
  document.addEventListener("cartUpdated", renderSummary);
  window.addEventListener("pagehide", () => {
    generation++;
    clearTimeout(timer);
  });
  setInterval(tick, 1000);
  const saved = pathnameID
    ? shop.read("bliss_order_" + decodeURIComponent(pathnameID))
    : shop.read("bliss_active_order");
  if (
    saved?.id &&
    (!pathnameID || saved.id === decodeURIComponent(pathnameID))
  ) {
    active = saved;
    retry = shop.read("bliss_retry_" + active.id)?.key || null;
  } else if (pathnameID)
    active = {
      id: decodeURIComponent(pathnameID),
      token: "",
      authenticated: false,
      items: {},
    };
  if (active) {
    $("checkoutWorkspace").hidden = true;
    $("orderJourney").hidden = false;
    $("journeyOrderID").textContent = active.id;
    shop.ready.then(refresh);
  } else {
    if (shop.read("bliss_pending_order")) {
      message(
        "An earlier order request is unconfirmed. Recover the saved request before creating another order.",
      );
      $("placeOrder").textContent = "Recover saved order request";
    }
    renderSummary();
  }
})();
