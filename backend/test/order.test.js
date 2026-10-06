import test from "node:test";
import assert from "node:assert/strict";
import { priceOrderItems } from "../src/services/order.js";

test("prices products from the server catalog and ignores client totals", () => {
  const order = priceOrderItems([
    { id: "p1", qty: 2, name: "Free perfume", priceKes: 1 },
    { id: "p23", qty: 1, priceKes: 0 },
  ]);

  assert.equal(order.totalKes, 6000);
  assert.deepEqual(order.items[0], {
    id: "p1",
    name: "Arabian Wood",
    size: "100ml",
    priceKes: 2000,
    qty: 2,
  });
});

test("rejects unknown products and invalid quantities", () => {
  assert.throws(() => priceOrderItems([{ id: "unknown", qty: 1 }]), /Unknown product/);
  assert.throws(() => priceOrderItems([{ id: "p1", qty: 0 }]), /Invalid quantity/);
});