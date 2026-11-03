const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const fs = require("node:fs");

function createMockDOM(catalogData = null) {
  const listeners = {};
  const elements = {};

  function makeEl(id = "") {
    const el = {
      id,
      innerHTML: "",
      textContent: "",
      value: id.toLowerCase().includes("select") ? "all" : "",
      disabled: false,
      dataset: {},
      children: [],
      style: {},
      classList: {
        add: () => {},
        remove: () => {},
        toggle: () => {},
        contains: () => false,
      },
      addEventListener: (e, cb) => {
        el._listeners = el._listeners || {};
        el._listeners[e] = el._listeners[e] || [];
        el._listeners[e].push(cb);
      },
      appendChild: (child) => {
        el.children.push(child);
        return child;
      },
      querySelectorAll: (sel) => {
        if (sel.includes("product-thumb-btn")) return [];
        return [];
      },
      querySelector: () => null,
      setAttribute: (k, v) => {
        el[k] = v;
      },
      getAttribute: (k) => el[k] || null,
    };
    return el;
  }

  return {
    addEventListener: (e, cb) => {
      listeners[e] = listeners[e] || [];
      listeners[e].push(cb);
    },
    dispatchEvent: (e) => {
      (listeners[e.type] || []).forEach((cb) => cb(e));
    },
    querySelector: (sel) => {
      if (sel === 'meta[name="api-base-url"]') {
        return { content: "" };
      }
      return null;
    },
    querySelectorAll: () => [],
    getElementById: (id) => {
      if (id === "catalogData" && catalogData) {
        return { textContent: JSON.stringify(catalogData) };
      }
      if (!elements[id]) {
        elements[id] = makeEl(id);
      }
      return elements[id];
    },
    createElement: (tag) => makeEl(tag),
    body: makeEl("body"),
  };
}

test("database prices load, failures preserve products while gating purchases, stale carts block", async () => {
  let response;
  const context = vm.createContext({
    document: createMockDOM(),
    window: {
      location: { href: "" },
      addEventListener: () => {},
      scrollTo: () => {},
      scrollY: 0,
    },
    localStorage: { getItem: () => null, setItem: () => {} },
    setTimeout: () => {},
    setInterval: () => {},
    AbortSignal,
    Event: class {
      constructor(t) {
        this.type = t;
      }
    },
    Date,
    fetch: async () => response,
  });
  vm.runInContext(
    fs.readFileSync(__dirname + "/static/shared.js", "utf8"),
    context,
  );
  const product = {
    id: "p1",
    name: "A <perfume>",
    category: "Woody",
    gender: "Unisex",
    size: "100ml",
    notes: "",
    description: "",
    priceKes: 2500,
    available: true,
    images: ["img/arabian%20wood.jpeg"],
  };

  // 1. Initial failure with no prior catalog results in unavailable empty catalog
  response = { ok: false };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext("PRODUCTS.length", context), 0);
  assert.equal(vm.runInContext("catalogState", context), "unavailable");
  assert.equal(vm.runInContext("catalogFresh()", context), false);

  // 2. Successful fetch transitions to ready and loads products
  response = { ok: true, json: async () => ({ products: [product] }) };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext('getProduct("p1").priceKes', context), 2500);
  assert.equal(
    vm.runInContext('getProduct("p1").name', context),
    "A <perfume>",
  );
  assert.equal(vm.runInContext("catalogState", context), "ready");
  assert.equal(vm.runInContext("catalogFresh()", context), true);

  vm.runInContext("cart = {p1: 2}", context);
  assert.equal(vm.runInContext("cartTotal()", context), 5000);
  assert.equal(vm.runInContext("cartAvailable()", context), true);

  // 3. Stale cache age blocks purchasing
  vm.runInContext("catalogLoadedAt = Date.now() - 61000", context);
  assert.equal(vm.runInContext("cartAvailable()", context), false);
  assert.equal(vm.runInContext("catalogFresh()", context), false);

  // 4. API refresh failure preserves existing products for browsing but marks catalog stale and gates purchases
  response = { ok: false };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext("PRODUCTS.length", context), 1);
  assert.equal(vm.runInContext("catalogState", context), "stale");
  assert.equal(vm.runInContext("catalogFresh()", context), false);
  assert.equal(vm.runInContext("cartAvailable()", context), false);

  // Attempting to add to cart during stale catalog is gated
  vm.runInContext("addToCart('p1')", context);
  assert.equal(vm.runInContext("cart.p1", context), 2);

  // 5. Unsafe media response is rejected; existing valid catalog is preserved and kept stale
  response = {
    ok: true,
    json: async () => ({
      products: [{ ...product, images: ["javascript:alert(1)"] }],
    }),
  };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext("PRODUCTS.length", context), 1);
  assert.equal(vm.runInContext("catalogState", context), "stale");
  assert.equal(vm.runInContext("catalogFresh()", context), false);
});

test("SSR products preserved during API refresh failure while gating purchases and allowing browsing", async () => {
  const sharedCode = fs.readFileSync(__dirname + "/static/shared.js", "utf8");
  const appCode = fs.readFileSync(__dirname + "/static/app.js", "utf8");

  const sampleCatalog = [
    {
      id: "p1",
      name: "Arabian Wood",
      category: "Woody",
      gender: "Unisex",
      size: "100ml",
      notes: "Sandalwood, Cedar",
      description: "Warm woody scent",
      priceKes: 2500,
      available: true,
      images: ["img/arabian%20wood.jpeg"],
    },
  ];

  const dom = createMockDOM(sampleCatalog);
  let response = { ok: false };
  const context = vm.createContext({
    document: dom,
    window: {
      location: { href: "" },
      addEventListener: () => {},
      scrollTo: () => {},
      scrollY: 0,
    },
    localStorage: {
      getItem: () => "{}",
      setItem: () => {},
    },
    setTimeout: () => {},
    setInterval: () => {},
    AbortSignal,
    Event: class {
      constructor(t) {
        this.type = t;
      }
    },
    Date,
    fetch: async () => response,
    console,
  });

  // Load shared.js which parses SSR embedded catalogData
  vm.runInContext(sharedCode, context);
  assert.equal(vm.runInContext("PRODUCTS.length", context), 1);
  assert.equal(vm.runInContext("catalogState", context), "ready");

  // Load app.js
  vm.runInContext(appCode, context);

  // Background API refresh fails (e.g. backend API unavailable during SSR fallback)
  response = { ok: false };
  await vm.runInContext("loadCatalog()", context);

  // SSR products must remain preserved in memory for browsing
  assert.equal(
    vm.runInContext("PRODUCTS.length", context),
    1,
    "PRODUCTS must remain preserved when API refresh fails",
  );
  assert.equal(
    vm.runInContext("catalogState", context),
    "stale",
    "catalogState must be stale on refresh failure",
  );
  assert.equal(
    vm.runInContext("catalogFresh()", context),
    false,
    "catalogFresh() must be false when catalogState is stale",
  );
  assert.equal(
    vm.runInContext("cartAvailable()", context),
    false,
    "cartAvailable() must gate purchases when stale",
  );

  // Product cards remain rendered in productGrid; NOT replaced with "Collection unavailable"
  const gridEl = dom.getElementById("productGrid");
  assert.doesNotMatch(
    gridEl.textContent,
    /Collection unavailable/,
    "productGrid must not be replaced with unavailable message when SSR products exist",
  );
  assert.match(
    gridEl.innerHTML,
    /Arabian Wood/,
    "productGrid must keep rendered products visible for browsing",
  );
  assert.match(
    gridEl.innerHTML,
    /Prices unavailable/,
    "product card button must indicate prices unavailable and gate purchasing",
  );

  // Product modal opens for browsing details even when stale, with purchase gated
  vm.runInContext("openProductModal('p1')", context);
  const modalName = dom.getElementById("modalName");
  const modalAdd = dom.getElementById("modalAddToCart");
  assert.equal(modalName.textContent, "Arabian Wood");
  assert.equal(
    modalAdd.disabled,
    true,
    "modal add to cart button must be disabled when catalog is stale",
  );
  assert.equal(
    modalAdd.textContent,
    "Prices unavailable",
    "modal add to cart button must show Prices unavailable",
  );

  // Calling addToCart directly during stale catalog must be a no-op
  vm.runInContext("addToCart('p1')", context);
  assert.equal(
    vm.runInContext("cart.p1", context),
    undefined,
    "cart must not accept items while catalog is stale",
  );
});

test("page scripts (app.js, cart.js, checkout.js) execute cleanly after shared.js, fail if loaded before", () => {
  const sharedCode = fs.readFileSync(__dirname + "/static/shared.js", "utf8");
  const appCode = fs.readFileSync(__dirname + "/static/app.js", "utf8");
  const cartCode = fs.readFileSync(__dirname + "/static/cart.js", "utf8");
  const checkoutCode = fs.readFileSync(
    __dirname + "/static/checkout.js",
    "utf8",
  );

  const sampleCatalog = [
    {
      id: "p1",
      name: "Arabian Wood",
      category: "Woody",
      gender: "Unisex",
      size: "100ml",
      notes: "Sandalwood, Cedar",
      description: "Warm woody scent",
      priceKes: 2500,
      available: true,
      images: ["img/arabian%20wood.jpeg"],
    },
  ];

  function makeContext(withCatalog = true) {
    return vm.createContext({
      document: createMockDOM(withCatalog ? sampleCatalog : null),
      window: {
        location: { href: "" },
        addEventListener: () => {},
        scrollTo: () => {},
        scrollY: 0,
      },
      localStorage: {
        getItem: () => JSON.stringify({ p1: 1 }),
        setItem: () => {},
      },
      setTimeout: () => {},
      setInterval: () => {},
      AbortSignal,
      Event: class {
        constructor(t) {
          this.type = t;
        }
      },
      Date,
      fetch: async () => ({ ok: true, json: async () => ({ products: [] }) }),
      console,
    });
  }

  // 1. Without shared.js first, page scripts must fail with ReferenceError
  const bareContext1 = makeContext();
  assert.throws(
    () => vm.runInContext(appCode, bareContext1),
    /ReferenceError/,
    "app.js must throw ReferenceError if shared.js is missing",
  );

  const bareContext2 = makeContext();
  assert.throws(
    () => vm.runInContext(cartCode, bareContext2),
    /ReferenceError/,
    "cart.js must throw ReferenceError if shared.js is missing",
  );

  const bareContext3 = makeContext();
  assert.throws(
    () => vm.runInContext(checkoutCode, bareContext3),
    /ReferenceError/,
    "checkout.js must throw ReferenceError if shared.js is missing",
  );

  // 2. With shared.js loaded first, each page script must initialize cleanly
  const ctxIndex = makeContext(true);
  vm.runInContext(sharedCode, ctxIndex);
  assert.doesNotThrow(
    () => vm.runInContext(appCode, ctxIndex),
    "app.js should run cleanly after shared.js",
  );

  const ctxCart = makeContext(true);
  vm.runInContext(sharedCode, ctxCart);
  assert.doesNotThrow(
    () => vm.runInContext(cartCode, ctxCart),
    "cart.js should run cleanly after shared.js",
  );

  const ctxCheckout = makeContext(true);
  vm.runInContext(sharedCode, ctxCheckout);
  assert.doesNotThrow(
    () => vm.runInContext(checkoutCode, ctxCheckout),
    "checkout.js should run cleanly after shared.js",
  );
});
