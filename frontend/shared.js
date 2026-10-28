// --------------------
// CONFIG
// --------------------
const SHOP_WHATSAPP_NUMBER = "254796382024"; // format: 2547...

// --------------------
// PRODUCTS
// --------------------
let PRODUCTS = [];
let catalogState = "loading";
let catalogLoadedAt = 0;
const CATALOG_MAX_AGE_MS = 60000;

function catalogFresh() {
  return catalogState === "ready" && Date.now() - catalogLoadedAt < CATALOG_MAX_AGE_MS;
}
function cartAvailable() {
  return catalogFresh() && Object.entries(cart).every(([id, qty]) =>
    Number.isInteger(qty) && qty > 0 && getProduct(id)?.available === true);
}
function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
}
async function loadCatalog() {
  catalogState = "loading";
  const base = (document.querySelector('meta[name="api-base-url"]')?.content.trim() || "").replace(/\/$/, "");
  try {
    const response = await fetch(`${base}/api/catalog`, {cache: "no-store", signal: AbortSignal.timeout(15000)});
    if (!response.ok) throw new Error("Catalog unavailable");
    const data = await response.json();
    if (!Array.isArray(data.products) || !data.products.length) throw new Error("Invalid catalog");
    const ids = new Set();
    PRODUCTS = data.products.map(p => {
      if (!/^p[0-9]+$/.test(p.id) || ids.has(p.id) || !Number.isSafeInteger(p.priceKes) || p.priceKes <= 0 || typeof p.available !== "boolean" || !Array.isArray(p.images)) throw new Error("Invalid product");
      ids.add(p.id);
      const safe = {...p};
      for (const field of ["name", "category", "gender", "size", "notes", "description"]) {
        if (typeof p[field] !== "string") throw new Error("Invalid product field");
      }
      safe.images = p.images.map(url => {
        if (typeof url !== "string" || !/^img\/[a-zA-Z0-9% ._-]+\.jpeg$/.test(url)) throw new Error("Invalid media path");
        return url;
      });
      return safe;
    });
    catalogState = "ready";
    catalogLoadedAt = Date.now();
  } catch {
    PRODUCTS = [];
    catalogState = "unavailable";
    setTimeout(loadCatalog, 1500);
  }
  document.dispatchEvent(new Event("catalogUpdated"));
}
// Refresh availability and prices; no browser-persisted price fallback.
setTimeout(loadCatalog, 0);
setInterval(loadCatalog, 30000);
window.addEventListener("online", loadCatalog);

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
  if (!catalogFresh() || getProduct(id)?.available !== true) return;
  cart[id] = (cart[id] || 0) + 1;
  saveCart(cart);
  document.dispatchEvent(new Event("cartUpdated"));
  const p = getProduct(id);
  if (p) showCartToast(p.name);
}

function setQty(id, qty) {
  if (qty > (cart[id] || 0) && (!catalogFresh() || getProduct(id)?.available !== true)) return;
  if (!Number.isInteger(qty)) return;
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
