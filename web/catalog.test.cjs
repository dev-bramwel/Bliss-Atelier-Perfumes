const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("node:vm");
const fs = require("node:fs");

function createMockDOM(catalogData = null) {
  const listeners = {};
  return {
    addEventListener: (e, cb) => {
      listeners[e] = listeners[e] || [];
      listeners[e].push(cb);
    },
    dispatchEvent: (e) => {
      (listeners[e.type] || []).forEach((cb) => cb(e));
    },
    querySelector: () => null,
    querySelectorAll: () => [],
    getElementById: (id) => {
      if (id === "catalogData" && catalogData) {
        return { textContent: JSON.stringify(catalogData) };
      }
      return null;
    },
    createElement: () => ({
      appendChild: () => {},
      querySelector: () => ({ textContent: "" }),
      classList: { add: () => {}, remove: () => {} },
    }),
    body: { appendChild: () => {} },
  };
}

test("database prices load, unsafe media and failures clear products, stale carts block", async () => {
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
  response = { ok: true, json: async () => ({ products: [product] }) };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext('getProduct("p1").priceKes', context), 2500);
  assert.equal(
    vm.runInContext('getProduct("p1").name', context),
    "A <perfume>",
  );
  vm.runInContext("cart = {p1: 2}", context);
  assert.equal(vm.runInContext("cartTotal()", context), 5000);
  assert.equal(vm.runInContext("cartAvailable()", context), true);
  vm.runInContext("catalogLoadedAt = Date.now() - 61000", context);
  assert.equal(vm.runInContext("cartAvailable()", context), false);
  response = { ok: false };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext("PRODUCTS.length", context), 0);
  assert.equal(vm.runInContext("catalogState", context), "unavailable");
  response = {
    ok: true,
    json: async () => ({
      products: [{ ...product, images: ["javascript:alert(1)"] }],
    }),
  };
  await vm.runInContext("loadCatalog()", context);
  assert.equal(vm.runInContext("PRODUCTS.length", context), 0);
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
