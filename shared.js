// --------------------
// CONFIG
// --------------------
const SHOP_WHATSAPP_NUMBER = "254796382024"; // format: 2547...

// --------------------
// PRODUCTS
// --------------------
const PRODUCTS = [
  {
    id: "p1",
    name: "Ocean Mist",
    priceKes: 2000,
    size: "50ml",
    category: "Fresh",
    gender: "Male",
    notes: "Citrus, clean, airy",
  },
  {
    id: "p2",
    name: "Vanilla Noir",
    priceKes: 2000,
    size: "50ml",
    category: "Sweet",
    gender: "Female",
    notes: "Vanilla, amber, cozy",
  },
  {
    id: "p3",
    name: "Rose & Oud",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    gender: "Female",
    notes: "Floral, oud, deep",
  },
  {
    id: "p4",
    name: "Spice Drift",
    priceKes: 2000,
    size: "100ml",
    category: "Spicy",
    gender: "Male",
    notes: "Pepper, woods, warm",
  },
  {
    id: "p5",
    name: "Cedar Night",
    priceKes: 2000,
    size: "50ml",
    category: "Woody",
    gender: "Male",
    notes: "Cedar, musk, dry",
  },
  {
    id: "p6",
    name: "Bloom Day",
    priceKes: 2000,
    size: "50ml",
    category: "Floral",
    gender: "Female",
    notes: "Jasmine, rose, soft",
  },
];

// --------------------
// PERSISTENCE
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
// STATE
// --------------------
let cart = loadCart();
let orders = loadOrders();

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

  const msg = ["Hi, I want to order:", ...items, ...extraLines].join("\n");
  return `https://wa.me/${SHOP_WHATSAPP_NUMBER}?text=${encodeURIComponent(msg)}`;
}

// --------------------
// CART OPS
// (each mutates state, saves, then fires "cartUpdated")
// --------------------
// --------------------
// CART TOAST NOTIFICATION
// --------------------
function showCartToast(productName) {
  let toast = document.getElementById("cart-toast");
  if (!toast) {
    toast = document.createElement("div");
    toast.id = "cart-toast";
    toast.className = "cart-toast";
    toast.innerHTML =
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><path d="M20.28 5.72a.75.75 0 0 0-1.06 0L9 15.94 4.78 11.72a.75.75 0 0 0-1.06 1.06l4.75 4.75a.75.75 0 0 0 1.06 0l10.75-10.75a.75.75 0 0 0 0-1.06Z"/></svg>` +
      `<span></span>`;
    document.body.appendChild(toast);
  }
  toast.querySelector("span").textContent = productName + " added to cart";
  if (toast._t) clearTimeout(toast._t);
  toast.classList.add("visible");
  toast._t = setTimeout(() => toast.classList.remove("visible"), 2400);
}

function addToCart(id) {
  cart[id] = (cart[id] || 0) + 1;
  saveCart(cart);
  document.dispatchEvent(new Event("cartUpdated"));
  const p = getProduct(id);
  if (p) showCartToast(p.name);
}

function setQty(id, qty) {
  if (qty <= 0) {
    delete cart[id];
  } else {
    cart[id] = qty;
  }
  saveCart(cart);
  document.dispatchEvent(new Event("cartUpdated"));
}

function clearCart() {
  cart = {};
  saveCart(cart);
  document.dispatchEvent(new Event("cartUpdated"));
}
