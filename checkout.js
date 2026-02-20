// --------------------
// DOM (checkout page)
// --------------------
const checkoutForm = document.getElementById("checkoutForm");
const checkoutTotalEl = document.getElementById("checkoutTotal");
const checkoutItemsEl = document.getElementById("checkoutItems");
const orderSuccess = document.getElementById("orderSuccess");
const backToTopBtn = document.getElementById("backToTopBtn");

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
checkoutForm?.addEventListener("submit", (e) => {
  e.preventDefault();
  if (cartCount() === 0) return;

  const fd = new FormData(checkoutForm);
  const customer = {
    name: String(fd.get("name") || ""),
    phone: String(fd.get("phone") || ""),
    location: String(fd.get("location") || ""),
    delivery: String(fd.get("delivery") || "delivery"),
    notes: String(fd.get("notes") || ""),
    payment: String(fd.get("payment") || "on_delivery"),
  };

  const order = {
    id: "ORD-" + Date.now(),
    createdAt: new Date().toISOString(),
    customer,
    items: Object.entries(cart).map(([id, qty]) => {
      const p = getProduct(id);
      return { id, name: p?.name, size: p?.size, priceKes: p?.priceKes, qty };
    }),
    totalKes: cartTotal(),
    status: "PENDING_PAYMENT",
  };

  orders.unshift(order);
  saveOrders(orders);

  // TODO: Send order to backend when ready
  // The order object is saved locally in localStorage until the backend is wired up.
  // Backend endpoint will replace this block: POST /api/orders with `order` payload.

  if (orderSuccess) orderSuccess.classList.remove("hidden");

  clearCart();
  checkoutForm.reset();

  // Redirect home after 5 seconds
  setTimeout(() => {
    window.location.href = "index.html";
  }, 10000);
});

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
