// --------------------
// CONFIG
// --------------------
const SHOP_WHATSAPP_NUMBER = "254796382024"; // replace later (format: 2547...)

// Example products (replace with real ones)
const PRODUCTS = [
  {
    id: "p1",
    name: "Ocean Mist",
    priceKes: 2000,
    size: "50ml",
    category: "Fresh",
    notes: "Citrus, clean, airy",
  },
  {
    id: "p2",
    name: "Vanilla Noir",
    priceKes: 2000,
    size: "50ml",
    category: "Sweet",
    notes: "Vanilla, amber, cozy",
  },
  {
    id: "p3",
    name: "Rose & Oud",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    notes: "Floral, oud, deep",
  },
  {
    id: "p4",
    name: "Spice Drift",
    priceKes: 2000,
    size: "100ml",
    category: "Spicy",
    notes: "Pepper, woods, warm",
  },
  {
    id: "p5",
    name: "Cedar Night",
    priceKes: 2000,
    size: "50ml",
    category: "Woody",
    notes: "Cedar, musk, dry",
  },
  {
    id: "p6",
    name: "Bloom Day",
    priceKes: 2000,
    size: "50ml",
    category: "Floral",
    notes: "Jasmine, rose, soft",
  },
];

// --------------------
// STATE
// --------------------
let cart = loadCart(); // { [id]: qty }
let orders = loadOrders(); // array

// --------------------
// DOM
// --------------------
const productGrid = document.getElementById("productGrid");
const resultsCount = document.getElementById("resultsCount");

const searchInput = document.getElementById("searchInput");
const categorySelect = document.getElementById("categorySelect");
const searchInputMobile = document.getElementById("searchInputMobile");
const categorySelectMobile = document.getElementById("categorySelectMobile");

const whatsappLink = document.getElementById("whatsappLink");
const openCartBtns = document.querySelectorAll("[data-open-cart]");
const closeCartBtn = document.getElementById("closeCartBtn");
const cartDrawer = document.getElementById("cartDrawer");
const cartBackdrop = document.getElementById("cartBackdrop");

const cartItemsEl = document.getElementById("cartItems");
const cartCountEls = document.querySelectorAll("[data-cart-count]");
const cartTotalEl = document.getElementById("cartTotal");
const openCheckoutBtn = document.getElementById("openCheckoutBtn");

const checkoutModal = document.getElementById("checkoutModal");
const checkoutBackdrop = document.getElementById("checkoutBackdrop");
const closeCheckoutBtn = document.getElementById("closeCheckoutBtn");
const checkoutForm = document.getElementById("checkoutForm");
const checkoutTotalEl = document.getElementById("checkoutTotal");
const orderSuccess = document.getElementById("orderSuccess");
const orderWhatsAppBtn = document.getElementById("orderWhatsAppBtn");

// --------------------
// HELPERS
// --------------------
function formatKes(n) {
  return String(n);
}

function getProduct(id) {
  return PRODUCTS.find((p) => p.id === id);
}

function cartCount() {
  return Object.values(cart).reduce((a, b) => a + b, 0);
}

function cartTotal() {
  let total = 0;
  for (const [id, qty] of Object.entries(cart)) {
    const p = getProduct(id);
    if (p) total += p.priceKes * qty;
  }
  return total;
}

function buildWhatsAppMessage(extraLines = []) {
  const items = Object.entries(cart)
    .map(([id, qty]) => {
      const p = getProduct(id);
      return p
        ? `- ${p.name} (${p.size}) x${qty} = KES ${p.priceKes * qty}`
        : "";
    })
    .filter(Boolean);

  const msg = [
    "Hi, I want to order:",
    ...items,
    `Total: KES ${cartTotal()}`,
    ...extraLines,
  ].join("\n");

  return `https://wa.me/${SHOP_WHATSAPP_NUMBER}?text=${encodeURIComponent(msg)}`;
}

// --------------------
// RENDER: Categories
// --------------------
function renderCategories() {
  const categories = [
    "all",
    ...Array.from(new Set(PRODUCTS.map((p) => p.category))),
  ];

  categorySelect.innerHTML = categories
    .map(
      (c) =>
        `<option value="${c}">${c === "all" ? "All categories" : c}</option>`,
    )
    .join("");

  categorySelectMobile.innerHTML = categories
    .map((c) => `<option value="${c}">${c === "all" ? "All" : c}</option>`)
    .join("");
}

// --------------------
// RENDER: Products with filters
// --------------------
function getFilters() {
  // desktop wins if visible, else mobile
  const s = (searchInput?.value ?? "").trim();
  const sm = (searchInputMobile?.value ?? "").trim();
  const search = s.length ? s : sm;

  const c = categorySelect?.value ?? "all";
  const cm = categorySelectMobile?.value ?? "all";
  const category = c !== "all" ? c : cm !== "all" ? cm : "all";

  return { search, category };
}

function renderProducts() {
  const { search, category } = getFilters();

  let filtered = PRODUCTS.slice();

  if (category !== "all") {
    filtered = filtered.filter((p) => p.category === category);
  }

  if (search.length) {
    const q = search.toLowerCase();
    filtered = filtered.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.notes.toLowerCase().includes(q) ||
        p.category.toLowerCase().includes(q),
    );
  }

  resultsCount.textContent = String(filtered.length);

  productGrid.innerHTML = filtered
    .map(
      (p) => `
    <div class="glass-panel rounded-3xl p-5 border border-white/10">
      <div class="flex items-start justify-between gap-3">
        <div>
          <div class="text-xs uppercase tracking-[0.35em] text-[var(--gold-soft)]">${p.category}</div>
          <h3 class="text-xl font-semibold text-white">${p.name}</h3>
          <p class="text-sm text-gray-400">${p.size} • ${p.notes}</p>
        </div>
        <div class="text-right">
          <div class="font-semibold text-lg text-[var(--gold-soft)]">KES ${formatKes(p.priceKes)}</div>
        </div>
      </div>

      <div class="mt-5 flex flex-col sm:flex-row gap-2">
        <button class="w-full sm:flex-1 py-2.5 rounded-2xl gold-btn text-sm" data-add="${p.id}">
          Add to cart
        </button>
        <a class="w-full sm:w-auto py-2.5 px-4 rounded-2xl border border-white/20 text-sm text-gray-100 text-center hover:bg-white/10"
           href="${buildWhatsAppMessage([`Item: ${p.name} (${p.size})`, `Price: KES ${p.priceKes}`])}"
           target="_blank" rel="noreferrer">
          WhatsApp
        </a>
      </div>
    </div>
  `,
    )
    .join("");

  document.querySelectorAll("[data-add]").forEach((btn) => {
    btn.addEventListener("click", () => addToCart(btn.dataset.add));
  });
}

// --------------------
// CART ops
// --------------------
function addToCart(id) {
  cart[id] = (cart[id] || 0) + 1;
  saveCart(cart);
  renderCart();
}

function setQty(id, qty) {
  if (qty <= 0) {
    delete cart[id];
  } else {
    cart[id] = qty;
  }
  saveCart(cart);
  renderCart();
}

function clearCart() {
  cart = {};
  saveCart(cart);
  renderCart();
}

// --------------------
// RENDER: Cart
// --------------------
function renderCart() {
  const count = cartCount();
  const total = cartTotal();

  cartCountEls.forEach((el) => (el.textContent = String(count)));
  cartTotalEl.textContent = formatKes(total);
  checkoutTotalEl.textContent = formatKes(total);

  openCheckoutBtn.disabled = count === 0;

  // top WhatsApp = cart message
  whatsappLink.href =
    count === 0
      ? `https://wa.me/${SHOP_WHATSAPP_NUMBER}?text=${encodeURIComponent("Hi, I want to ask about your perfumes.")}`
      : buildWhatsAppMessage(["My location:", "Preferred delivery time:"]);

  const entries = Object.entries(cart);

  if (entries.length === 0) {
    cartItemsEl.innerHTML = `<p class="text-gray-400 text-sm">Cart is empty. Tragic.</p>`;
    return;
  }

  cartItemsEl.innerHTML = entries
    .map(([id, qty]) => {
      const p = getProduct(id);
      if (!p) return "";
      return `
      <div class="glass-panel rounded-2xl p-4 flex items-center justify-between gap-3 border border-white/10">
        <div>
          <div class="font-medium text-white">${p.name}</div>
          <div class="text-sm text-gray-300">${p.size} • KES ${p.priceKes}</div>
          <div class="text-xs text-gray-500">${p.category} • ${p.notes}</div>
        </div>

        <div class="flex items-center gap-2">
          <button class="px-2 py-1 rounded-2xl border border-white/20 text-white" data-dec="${id}">-</button>
          <input class="w-16 text-center glass-field rounded-2xl py-1" type="number" min="1" value="${qty}" data-qty="${id}" />
          <button class="px-2 py-1 rounded-2xl border border-white/20 text-white" data-inc="${id}">+</button>
        </div>
      </div>
    `;
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
// Drawer & Modal controls
// --------------------
function openCart() {
  cartDrawer.classList.remove("hidden");
}
function closeCart() {
  cartDrawer.classList.add("hidden");
}

function openCheckout() {
  orderSuccess.classList.add("hidden");
  checkoutModal.classList.remove("hidden");
}
function closeCheckout() {
  checkoutModal.classList.add("hidden");
}

openCartBtns.forEach((btn) => btn.addEventListener("click", openCart));
closeCartBtn.addEventListener("click", closeCart);
cartBackdrop.addEventListener("click", closeCart);

openCheckoutBtn.addEventListener("click", () => {
  if (cartCount() === 0) return;
  closeCart();
  openCheckout();
});

checkoutBackdrop.addEventListener("click", closeCheckout);
closeCheckoutBtn.addEventListener("click", closeCheckout);

// --------------------
// Checkout submit: create order (local only for now)
// --------------------
checkoutForm.addEventListener("submit", (e) => {
  e.preventDefault();
  if (cartCount() === 0) return;

  const fd = new FormData(checkoutForm);
  const customer = {
    name: String(fd.get("name") || ""),
    phone: String(fd.get("phone") || ""),
    location: String(fd.get("location") || ""),
    delivery: String(fd.get("delivery") || "delivery"),
    notes: String(fd.get("notes") || ""),
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

  // Create WhatsApp link for THIS order
  const lines = [
    `Order ID: ${order.id}`,
    `Name: ${customer.name}`,
    `Phone: ${customer.phone}`,
    `Location: ${customer.location}`,
    `Delivery: ${customer.delivery}`,
    customer.notes ? `Notes: ${customer.notes}` : null,
  ].filter(Boolean);

  orderWhatsAppBtn.href = buildWhatsAppMessage(lines);

  orderSuccess.classList.remove("hidden");

  // Optional: clear cart after saving order
  clearCart();

  // Reset form fields (keeps it neat)
  checkoutForm.reset();
});

// --------------------
// Filters events
// --------------------
[searchInput, searchInputMobile].forEach((inp) =>
  inp?.addEventListener("input", renderProducts),
);
[categorySelect, categorySelectMobile].forEach((sel) =>
  sel?.addEventListener("change", renderProducts),
);

// --------------------
// Persistence
// --------------------
function loadCart() {
  try {
    return JSON.parse(localStorage.getItem("cart") || "{}");
  } catch {
    return {};
  }
}
function saveCart(c) {
  localStorage.setItem("cart", JSON.stringify(c));
}
function loadOrders() {
  try {
    return JSON.parse(localStorage.getItem("orders") || "[]");
  } catch {
    return [];
  }
}
function saveOrders(o) {
  localStorage.setItem("orders", JSON.stringify(o));
}

// --------------------
// INIT
// --------------------
renderCategories();
renderProducts();
renderCart();
