// --------------------
// CONFIG
// --------------------
const SHOP_WHATSAPP_NUMBER = "254796382024"; // format: 2547...

// Shared connection protection. Writes are never queued or replayed: an uncertain
// response must be checked against the server before an operator retries it.
window.BlissConnection = (() => {
  let offline = false;
  let checking = false;
  let previousFocus = null;
  const overlay = document.getElementById("offlineOverlay");
  const admin = /^\/(admin(?:\/|$)|dashboard(?:\.html)?$)/.test(
    window.location.pathname || "",
  );
  const emit = () => {
    if (typeof window.dispatchEvent === "function")
      window.dispatchEvent(new Event("bliss:connection"));
  };
  function protectButtons() {
    document.querySelectorAll("[data-mutation]").forEach((button) => {
      if (offline) {
        if (button.dataset.offlineDisabled === undefined)
          button.dataset.offlineDisabled = String(button.disabled);
        button.disabled = true;
      } else if (button.dataset.offlineDisabled !== undefined) {
        button.disabled = button.dataset.offlineDisabled === "true";
        delete button.dataset.offlineDisabled;
      }
    });
  }
  function dropped() {
    const first = !offline;
    offline = true;
    protectButtons();
    if (overlay) {
      if (first) previousFocus = document.activeElement;
      overlay.hidden = false;
      overlay.dataset.restored = "false";
      const description = document.getElementById("offlineDescription");
      if (description && admin)
        description.textContent =
          "Atelier management connection dropped. Active dashboard inputs and drafts are held safely in memory.";
      else if (description)
        description.textContent =
          "Your connection to the atelier dropped. Your cart and typed details are preserved. Reservation time continues while offline.";
      const badge = document.getElementById("offlineBadge");
      if (badge) badge.textContent = "Connection retrying...";
      if (typeof overlay.showModal === "function" && !overlay.open)
        overlay.showModal();
      if (first && typeof overlay.focus === "function") overlay.focus();
    }
    emit();
  }
  async function heartbeat() {
    if (!offline || checking) return;
    if (typeof navigator !== "undefined" && navigator.onLine === false) return;
    checking = true;
    try {
      const response = await fetch("/api/health/live", {
        cache: "no-store",
        credentials: "same-origin",
        signal: AbortSignal.timeout(2500),
      });
      const data = response.ok ? await response.json() : null;
      if (data?.status !== "alive") return;
      offline = false;
      if (overlay) overlay.dataset.restored = "true";
      const badge = document.getElementById("offlineBadge");
      if (badge) badge.textContent = "Atelier Online ✓";
      protectButtons();
      emit();
      setTimeout(() => {
        if (offline || !overlay) return;
        if (typeof overlay.close === "function") overlay.close();
        overlay.hidden = true;
        if (previousFocus?.isConnected) previousFocus.focus();
      }, 800);
    } catch {
      /* The next bounded heartbeat will retry; form DOM stays intact. */
    } finally {
      checking = false;
    }
  }
  window.addEventListener("offline", dropped);
  window.addEventListener("online", heartbeat);
  overlay?.addEventListener("cancel", (event) => {
    if (offline) event.preventDefault();
  });
  const video = document.getElementById("offlineVideo");
  video?.addEventListener("loadeddata", () => {
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;
    video.hidden = false;
    const fallback = document.getElementById("offlineAtomizer");
    if (fallback) fallback.hidden = true;
  });
  video?.addEventListener("error", () => {
    video.hidden = true;
    const fallback = document.getElementById("offlineAtomizer");
    if (fallback) fallback.hidden = false;
  });
  if (typeof navigator !== "undefined" && navigator.onLine === false) dropped();
  setInterval(heartbeat, 3000);
  return {
    get offline() {
      return offline;
    },
    dropped,
    heartbeat,
    protectButtons,
  };
})();

// --------------------
// PRODUCTS
// --------------------
let PRODUCTS = [];
let catalogState = "loading";
let catalogLoadedAt = 0;
const CATALOG_MAX_AGE_MS = 60000;

function catalogFresh() {
  return (
    catalogState === "ready" &&
    Date.now() - catalogLoadedAt < CATALOG_MAX_AGE_MS
  );
}
function cartAvailable() {
  return (
    catalogFresh() &&
    Object.entries(cart).every(
      ([id, qty]) =>
        Number.isInteger(qty) &&
        qty > 0 &&
        qty <= 100 &&
        getProduct(id)?.available === true &&
        (!Number.isInteger(getProduct(id)?.stock) ||
          qty <= getProduct(id).stock),
    )
  );
}
function escapeHTML(value) {
  return String(value).replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
}
async function loadCatalog() {
  if (window.BlissConnection.offline) return;
  if (PRODUCTS.length === 0) {
    catalogState = "loading";
  }
  try {
    const response = await fetch(`/api/catalog`, {
      cache: "no-store",
      signal: AbortSignal.timeout(15000),
    });
    if (!response.ok) {
      if (response.status >= 500) window.BlissConnection.dropped();
      throw new Error("Catalog unavailable");
    }
    const data = await response.json();
    if (!Array.isArray(data.products) || !data.products.length)
      throw new Error("Invalid catalog");
    const ids = new Set();
    const safe = data.products.map((p) => {
      if (
        !/^p[0-9]+$/.test(p.id) ||
        ids.has(p.id) ||
        !Number.isSafeInteger(p.priceKes) ||
        p.priceKes <= 0 ||
        typeof p.available !== "boolean" ||
        !Array.isArray(p.images)
      )
        throw new Error("Invalid product");
      ids.add(p.id);
      const item = { ...p };
      for (const field of [
        "name",
        "category",
        "gender",
        "size",
        "notes",
        "description",
      ]) {
        if (typeof p[field] !== "string")
          throw new Error("Invalid product field");
      }
      item.images = p.images.map((url) => {
        if (
          typeof url !== "string" ||
          !/^img\/[a-zA-Z0-9% ._-]+\.jpeg$/.test(url)
        )
          throw new Error("Invalid media path");
        return url;
      });
      return item;
    });
    PRODUCTS = safe;
    catalogState = "ready";
    catalogLoadedAt = Date.now();
  } catch (error) {
    if (error instanceof TypeError || error.name === "TimeoutError")
      window.BlissConnection.dropped();
    if (PRODUCTS.length > 0) {
      catalogState = "stale";
    } else {
      PRODUCTS = [];
      catalogState = "unavailable";
    }
    setTimeout(loadCatalog, 1500);
  }
  document.dispatchEvent(new Event("catalogUpdated"));
}
// Refresh availability and prices; no browser-persisted price fallback.
function initEmbeddedCatalog() {
  const el = document.getElementById("catalogData");
  if (!el) return;
  try {
    const raw = JSON.parse(el.textContent);
    if (Array.isArray(raw) && raw.length > 0) {
      PRODUCTS = raw;
      catalogState = "ready";
      catalogLoadedAt = Date.now();
    }
  } catch {}
}
initEmbeddedCatalog();
setTimeout(loadCatalog, 0);
setInterval(loadCatalog, 30000);
window.addEventListener("online", loadCatalog);
window.addEventListener("bliss:connection", () => {
  if (!window.BlissConnection.offline) loadCatalog();
});

// --------------------
// PERSISTENCE
// --------------------
function loadCart() {
  try {
    const saved = JSON.parse(localStorage.getItem("cart") || "{}");
    if (!saved || Array.isArray(saved) || typeof saved !== "object") return {};
    return Object.fromEntries(
      Object.entries(saved).filter(
        ([id, qty]) =>
          /^p[0-9]+$/.test(id) &&
          Number.isInteger(qty) &&
          qty > 0 &&
          qty <= 100,
      ),
    );
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
  if ((cart[id] || 0) >= Math.min(getProduct(id).stock ?? 100, 100)) return;
  cart[id] = (cart[id] || 0) + 1;
  saveCart(cart);
  document.dispatchEvent(new Event("cartUpdated"));
  const p = getProduct(id);
  if (p) showCartToast(p.name);
}

function setQty(id, qty) {
  if (qty > Math.min(getProduct(id)?.stock ?? 100, 100)) return;
  if (
    qty > (cart[id] || 0) &&
    (!catalogFresh() || getProduct(id)?.available !== true)
  )
    return;
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

// --------------------
// CART BADGES & NAVIGATION
// --------------------
function updateCartBadges() {
  const count = cartCount();
  document.querySelectorAll("[data-cart-count]").forEach((el) => {
    el.textContent = String(count);
  });
}
document.addEventListener("cartUpdated", updateCartBadges);
updateCartBadges();

document.querySelectorAll("[data-open-cart]").forEach((btn) => {
  btn.addEventListener("click", () => {
    window.location.href = "/cart";
  });
});
