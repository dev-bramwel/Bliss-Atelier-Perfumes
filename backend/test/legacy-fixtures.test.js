import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { priceOrderItems } from "../src/services/order.js";

const fixtures = JSON.parse(readFileSync(new URL("../testdata/legacy/contracts.json", import.meta.url)));

test("legacy checkout fixture retains server pricing despite client tampering", () => {
  assert.deepEqual(priceOrderItems(fixtures.checkout.items), {
    items: fixtures.created.order.items,
    totalKes: fixtures.created.order.totalKes,
  });
});

test("captures all 23 legacy product IDs as historical KES 2000 observations", () => {
  const names = ["Arabian Wood", "Aventus Man", "Azzaro Chrome", "Blue Man", "Chocolate", "Eros", "Guilty Woman", "Interlude Man", "Lost Cherry", "Man Extreme", "Men in Black", "Mon Legend", "Oud Ispahan", "Rouge 540", "Royal Night", "Sauvage Elixir", "Sauvage", "Scandal Man", "Splendid Vanilla", "Stronger with You Oud", "Tere DH", "Tobacco Vanilla", "Vanilla"];
  const result = priceOrderItems(names.map((_, index) => ({id: `p${index+1}`, qty: 1})));
  assert.equal(result.totalKes, 46000);
  assert.deepEqual(result.items.map(item => item.name), names);
  assert.ok(result.items.every(item => item.priceKes === 2000 && item.size === "100ml"));
});

test("safe legacy quantity and item bounds", () => {
  for (const qty of [0, -1, 101, 1.5, "1", null]) assert.throws(() => priceOrderItems([{id: "p1", qty}]));
  for (const items of [[], Array(51).fill({id: "p1", qty: 1}), [null], [{id: "unknown", qty: 1}]]) assert.throws(() => priceOrderItems(items));
  assert.equal(priceOrderItems(Array(50).fill({id: "p1", qty: 100})).totalKes, 10000000);
});
