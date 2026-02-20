// --------------------
// CONFIG
// --------------------
const API_BASE = "http://localhost:5000"; // change to your deployed API URL in production

// --------------------
// DOM (checkout page)
// --------------------
const checkoutForm   = document.getElementById("checkoutForm");
const checkoutTotalEl = document.getElementById("checkoutTotal");
const checkoutItemsEl = document.getElementById("checkoutItems");
const orderSuccess   = document.getElementById("orderSuccess");
const backToTopBtn   = document.getElementById("backToTopBtn");
const stkOverlay     = document.getElementById("stkOverlay");
const stkStatusText  = document.getElementById("stkStatusText");
const stkSubText     = document.getElementById("stkSubText");

// --------------------
// RENDER ORDER SUMMARY
// --------------------
function renderCheckoutSummary() {
  const count = cartCount();
  const total = cartTotal();

  // If cart is empty, send back to shop
  if (count === 0) {
    window.location.href = "index.html";
    return;
  }

  if (checkoutTotalEl) checkoutTotalEl.textContent = formatKes(total);

  if (!checkoutItemsEl) return;

  checkoutItemsEl.innerHTML = Object.entries(cart)
    .map(([id, qty]) => {
      const p = getProduct(id);
      if (!p) return "";
      return `
        <div class="flex items-center justify-between py-2 border-b border-white/5">
          <div class="min-w-0 mr-3">
            <span class="text-white text-sm font-medium">${p.name}</span>
            <span class="text-gray-400 text-xs ml-1.5">${p.size} × ${qty}</span>
          </div>
          <span class="text-[var(--gold-soft)] text-sm shrink-0">KES ${formatKes(p.priceKes * qty)}</span>
        </div>`;
    })
    .join("");
}

// --------------------
// BACK TO TOP
// --------------------
function updateBackToTopVisibility() {
  if (!backToTopBtn) return;
  if (window.scrollY > 220) {
    backToTopBtn.classList.add("visible");
  } else {
    backToTopBtn.classList.remove("visible");
  }
}

backToTopBtn?.addEventListener("click", () =>
  window.scrollTo({ top: 0, behavior: "smooth" }),
);
window.addEventListener("scroll", updateBackToTopVisibility, { passive: true });

// --------------------
// CHECKOUT SUBMIT
// --------------------
checkoutForm?.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (cartCount() === 0) return;

  const fd = new FormData(checkoutForm);
  const customer = {
    name:     String(fd.get("name")     || ""),
    phone:    String(fd.get("phone")    || ""),
    location: String(fd.get("location") || ""),
    delivery: String(fd.get("delivery") || "delivery"),
    notes:    String(fd.get("notes")    || ""),
    payment:  String(fd.get("payment")  || "on_delivery"),
  };

  const orderPayload = {
    customer,
    items: Object.entries(cart).map(([id, qty]) => {
      const p = getProduct(id);
      return { id, name: p?.name, size: p?.size, priceKes: p?.priceKes, qty };
    }),
    totalKes: cartTotal(),
  };

  // Disable submit while processing
  const submitBtn = checkoutForm.querySelector("[type='submit']");
  if (submitBtn) { submitBtn.disabled = true; submitBtn.textContent = "Placing order…"; }

  try {
    const res = await fetch(`${API_BASE}/api/orders`, {
      method:  "POST",
      headers: { "Content-Type": "application/json" },
      body:    JSON.stringify(orderPayload),
    });

    const data = await res.json();

    if (!res.ok) {
      throw new Error(data.error || "Order submission failed.");
    }

    // Save order locally as a backup copy
    const localOrder = { ...data.order, _savedAt: new Date().toISOString() };
    orders.unshift(localOrder);
    saveOrders(orders);

    clearCart();
    checkoutForm.reset();

    if (customer.payment === "pay_now" && data.stkPush) {
      // ---- STK push sent — show waiting overlay and poll for result ----
      showStkOverlay();
      await pollPaymentStatus(data.order.id);
    } else {
      // ---- on_delivery or STK failed gracefully ----
      showOrderSuccess();
    }

  } catch (err) {
    console.error("[checkout submit]", err);
    alert(`Could not place order: ${err.message}\n\nPlease try again or contact us on WhatsApp.`);
  } finally {
    if (submitBtn) { submitBtn.disabled = false; submitBtn.textContent = "Place Order"; }
  }
});

// --------------------
// STK PUSH OVERLAY
// --------------------
function showStkOverlay() {
  if (!stkOverlay) return;
  stkOverlay.classList.remove("hidden");
}

function hideStkOverlay() {
  if (!stkOverlay) return;
  stkOverlay.classList.add("hidden");
}

function setStkStatus(title, sub) {
  if (stkStatusText) stkStatusText.textContent = title;
  if (stkSubText)    stkSubText.textContent    = sub;
}

/**
 * Polls GET /api/orders/:id/payment-status every 4 seconds for up to 2 minutes.
 * Resolves once M-Pesa responds or times out.
 */
async function pollPaymentStatus(orderId) {
  const MAX_TRIES  = 30;  // 30 × 4s = 2 minutes
  const INTERVAL   = 4000;
  let   tries      = 0;

  setStkStatus("Check your phone", "Enter your M-Pesa PIN when prompted to complete payment.");

  return new Promise((resolve) => {
    const timer = setInterval(async () => {
      tries++;

      try {
        const res  = await fetch(`${API_BASE}/api/orders/${orderId}/payment-status`);
        const data = await res.json();

        if (data.paymentStatus === "PAID") {
          clearInterval(timer);
          setStkStatus(
            "Payment confirmed ✓",
            `Receipt: ${data.receiptNumber ?? "–"}. Thank you for your order!`,
          );
          setTimeout(() => {
            hideStkOverlay();
            showOrderSuccess();
            resolve();
          }, 2500);
        } else if (data.paymentStatus === "FAILED") {
          clearInterval(timer);
          setStkStatus(
            "Payment unsuccessful",
            "The M-Pesa request was cancelled or timed out. Your order is saved — we'll be in touch.",
          );
          setTimeout(() => { hideStkOverlay(); showOrderSuccess(); resolve(); }, 3500);
        } else if (tries >= MAX_TRIES) {
          clearInterval(timer);
          setStkStatus(
            "Taking longer than expected…",
            "We haven't received M-Pesa confirmation yet. Your order is saved — we'll follow up.",
          );
          setTimeout(() => { hideStkOverlay(); showOrderSuccess(); resolve(); }, 3500);
        }
        // else still PENDING — keep polling
      } catch (err) {
        console.warn("[poll payment status]", err);
        // Network blip — keep trying until MAX_TRIES
        if (tries >= MAX_TRIES) {
          clearInterval(timer);
          hideStkOverlay();
          showOrderSuccess();
          resolve();
        }
      }
    }, INTERVAL);
  });
}

function showOrderSuccess() {
  if (orderSuccess) orderSuccess.classList.remove("hidden");
  setTimeout(() => { window.location.href = "index.html"; }, 10000);
}

// --------------------
// PAYMENT OPTION TOGGLE
// --------------------
(function () {
  const cards = document.querySelectorAll(".payment-option-card");
  const payNowNotice = document.getElementById("payNowNotice");

  function syncCards() {
    cards.forEach((card) => {
      const radio = card.querySelector("input[type='radio']");
      if (!radio) return;
      card.classList.toggle("selected", radio.checked);
    });
    const payNowRadio = document.querySelector("input[name='payment'][value='pay_now']");
    if (payNowNotice) {
      payNowNotice.classList.toggle("hidden", !(payNowRadio && payNowRadio.checked));
    }
  }

  cards.forEach((card) => {
    card.addEventListener("click", () => {
      const radio = card.querySelector("input[type='radio']");
      if (radio) radio.checked = true;
      syncCards();
    });
  });

  syncCards(); // set initial state
})();

// --------------------
// INIT
// --------------------
renderCheckoutSummary();
updateBackToTopVisibility();
