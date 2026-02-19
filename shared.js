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
function addToCart(id) {
  cart[id] = (cart[id] || 0) + 1;
  saveCart(cart);
  document.dispatchEvent(new Event("cartUpdated"));
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
