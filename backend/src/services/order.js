const PRODUCTS = new Map([
  ["p1", { name: "Arabian Wood", size: "100ml", priceKes: 2000 }],
  ["p2", { name: "Aventus Man", size: "100ml", priceKes: 2000 }],
  ["p3", { name: "Azzaro Chrome", size: "100ml", priceKes: 2000 }],
  ["p4", { name: "Blue Man", size: "100ml", priceKes: 2000 }],
  ["p5", { name: "Chocolate", size: "100ml", priceKes: 2000 }],
  ["p6", { name: "Eros", size: "100ml", priceKes: 2000 }],
  ["p7", { name: "Guilty Woman", size: "100ml", priceKes: 2000 }],
  ["p8", { name: "Interlude Man", size: "100ml", priceKes: 2000 }],
  ["p9", { name: "Lost Cherry", size: "100ml", priceKes: 2000 }],
  ["p10", { name: "Man Extreme", size: "100ml", priceKes: 2000 }],
  ["p11", { name: "Men in Black", size: "100ml", priceKes: 2000 }],
  ["p12", { name: "Mon Legend", size: "100ml", priceKes: 2000 }],
  ["p13", { name: "Oud Ispahan", size: "100ml", priceKes: 2000 }],
  ["p14", { name: "Rouge 540", size: "100ml", priceKes: 2000 }],
  ["p15", { name: "Royal Night", size: "100ml", priceKes: 2000 }],
  ["p16", { name: "Sauvage Elixir", size: "100ml", priceKes: 2000 }],
  ["p17", { name: "Sauvage", size: "100ml", priceKes: 2000 }],
  ["p18", { name: "Scandal Man", size: "100ml", priceKes: 2000 }],
  ["p19", { name: "Splendid Vanilla", size: "100ml", priceKes: 2000 }],
  ["p20", { name: "Stronger with You Oud", size: "100ml", priceKes: 2000 }],
  ["p21", { name: "Tere DH", size: "100ml", priceKes: 2000 }],
  ["p22", { name: "Tobacco Vanilla", size: "100ml", priceKes: 2000 }],
  ["p23", { name: "Vanilla", size: "100ml", priceKes: 2000 }],
]);

export function priceOrderItems(items) {
  if (!Array.isArray(items) || items.length === 0 || items.length > 50) {
    throw new Error("Order must contain between 1 and 50 items.");
  }

  const pricedItems = items.map((item) => {
    if (!item || typeof item.id !== "string") {
      throw new Error("Each order item must include a valid product ID.");
    }

    const product = PRODUCTS.get(item.id);
    if (!product) throw new Error(`Unknown product: ${item.id}`);
    if (!Number.isInteger(item.qty) || item.qty < 1 || item.qty > 100) {
      throw new Error(`Invalid quantity for product: ${item.id}`);
    }

    return { id: item.id, ...product, qty: item.qty };
  });

  return {
    items: pricedItems,
    totalKes: pricedItems.reduce(
      (total, item) => total + item.priceKes * item.qty,
      0,
    ),
  };
}