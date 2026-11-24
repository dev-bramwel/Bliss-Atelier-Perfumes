// --------------------
// DOM (cart page)
// --------------------
const cartItemsEl = document.getElementById("cartItems");
const cartTotalEl = document.getElementById("cartTotal");
const clearCartBtn = document.getElementById("clearCartBtn");
const toCheckoutBtn = document.getElementById("toCheckoutBtn");
const backToTopBtn = document.getElementById("backToTopBtn");
const whatsappLink = document.getElementById("whatsappLink");

// --------------------
// RENDER CART
// --------------------
function renderCart() {
  if (catalogState !== "ready") {
    if (cartItemsEl)
      cartItemsEl.textContent =
        catalogState === "loading"
          ? "Loading current prices..."
          : "Current prices unavailable. Please try again shortly.";
    if (cartTotalEl) cartTotalEl.textContent = "-";
    if (toCheckoutBtn) toCheckoutBtn.disabled = true;
    return;
  }
  const count = cartCount();
  const total = cartTotal();

  // Update count badges
  document.querySelectorAll("[data-cart-count]").forEach((el) => {
    el.textContent = String(count);
  });

  // Update total
  if (cartTotalEl) cartTotalEl.textContent = formatKes(total);

  // Toggle checkout button
  if (toCheckoutBtn) {
    toCheckoutBtn.disabled = count === 0 || !cartAvailable();
    toCheckoutBtn.classList.toggle("opacity-40", count === 0);
    toCheckoutBtn.classList.toggle("cursor-not-allowed", count === 0);
  }

  // Toggle clear button
  if (clearCartBtn) {
    clearCartBtn.disabled = count === 0;
    clearCartBtn.classList.toggle("opacity-40", count === 0);
    clearCartBtn.classList.toggle("cursor-not-allowed", count === 0);
  }

  // WhatsApp link
  if (whatsappLink) {
    whatsappLink.href =
      count === 0
        ? `https://wa.me/${SHOP_WHATSAPP_NUMBER}?text=${encodeURIComponent("Hi, I want to ask about your perfumes.")}`
        : buildWhatsAppMessage(["My location:", "Preferred delivery time:"]);
  }

  // Render items
  if (!cartItemsEl) return;

  const entries = Object.entries(cart);

  if (entries.length === 0) {
    cartItemsEl.innerHTML = `
      <div class="text-center py-16 space-y-5">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" class="w-12 h-12 mx-auto text-[#72634e]" fill="currentColor">
          <path d="M7 4h-.75a.75.75 0 0 0 0 1.5h.53l.63 2.52L9 14.25A2.25 2.25 0 0 0 11.2 16h5.18a2.25 2.25 0 0 0 2.2-1.75l1.2-5.4A1.25 1.25 0 0 0 18.56 7H8.75L8.2 4.98A1.5 1.5 0 0 0 7 4Zm3.05 4.5h8.51l-1.2 5.4a.75.75 0 0 1-.73.6h-5.18a.75.75 0 0 1-.73-.6L10.05 8.5ZM10 19.25a1.25 1.25 0 1 1-2.5 0 1.25 1.25 0 0 1 2.5 0Zm8 0a1.25 1.25 0 1 1-2.5 0 1.25 1.25 0 0 1 2.5 0Z" />
        </svg>
        <p class="text-[#c9bfb1] text-base">Your cart is empty.</p>
        <a href="/" class="inline-block px-6 py-3 rounded-[10px] gold-btn text-sm font-semibold">
          Browse Collection
        </a>
      </div>`;
    return;
  }

  cartItemsEl.innerHTML = entries
    .map(([id, qty]) => {
      const p = getProduct(id);
      if (!p) return "";
      return `
      <div class="atelier-card rounded-[14px] p-4 flex items-center justify-between gap-3 border border-[#4b4032] bg-[#241f19]">
        <div class="min-w-0">
          <div class="font-medium text-[#faf7f2] truncate">${escapeHTML(p.name)}</div>
          <div class="text-sm text-[#e6cfa9]">${escapeHTML(p.size)} • KES ${formatKes(p.priceKes)}</div>
          <div class="text-xs text-[#c8bba7]">${escapeHTML(p.gender ?? "Unisex")} • ${escapeHTML(p.category)} • ${escapeHTML(p.notes)}</div>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <button class="px-3 py-1 rounded-[8px] border border-[#72634e] bg-[#241f19] text-[#faf7f2] hover:bg-[#514534]" data-dec="${id}">−</button>
          <input
            class="w-14 text-center atelier-input rounded-[8px] py-1 text-sm bg-[#1c1813] border border-[#72634e] text-[#faf7f2]"
            type="number"
            min="1"
            value="${qty}"
            data-qty="${id}"
          />
          <button class="px-3 py-1 rounded-[8px] border border-[#72634e] bg-[#241f19] text-[#faf7f2] hover:bg-[#514534]" data-inc="${id}">+</button>
        </div>
      </div>`;
    })
    .join("");

  document
    .querySelectorAll("[data-dec]")
    .forEach((b) =>
      b.addEventListener("click", () =>
        setQty(b.dataset.dec, (cart[b.dataset.dec] || 1) - 1),
      ),
    );
  document
    .querySelectorAll("[data-inc]")
    .forEach((b) =>
      b.addEventListener("click", () =>
        setQty(b.dataset.inc, (cart[b.dataset.inc] || 1) + 1),
      ),
    );
  document
    .querySelectorAll("[data-qty]")
    .forEach((inp) =>
      inp.addEventListener("change", () =>
        setQty(inp.dataset.qty, Number(inp.value || 1)),
      ),
    );
}

// --------------------
// NAVIGATION
// --------------------
toCheckoutBtn?.addEventListener("click", () => {
  if (cartCount() === 0 || !cartAvailable()) return;
  window.location.href = "/checkout";
});

clearCartBtn?.addEventListener("click", () => {
  if (cartCount() === 0 || !cartAvailable()) return;
  clearCart();
});

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
// REACT TO CART CHANGES
// --------------------
document.addEventListener("cartUpdated", renderCart);

// --------------------
// INIT
// --------------------
renderCart();
updateBackToTopVisibility();

document.addEventListener("catalogUpdated", renderCart);
