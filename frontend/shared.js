// --------------------
// CONFIG
// --------------------
const SHOP_WHATSAPP_NUMBER = "254796382024"; // format: 2547...

// --------------------
// PRODUCTS
// --------------------
// TODO (production): replace `images` arrays with URLs from your database / CDN.
const PRODUCTS = [
  {
    id: "p1",
    name: "Arabian Wood",
    priceKes: 2000,
    size: "100ml",
    category: "Woody",
    gender: "Unisex",
    notes: "Oud, cedar, warm amber",
    description:
      "A journey through ancient souks and desert nights. Rich oud resin opens the composition, giving way to a heart of smoked cedarwood and precious resins. The base settles into warm amber and earthy musk — deeply intimate, endlessly alluring.",
    images: ["img/arabian%20wood.jpeg"],
  },
  {
    id: "p2",
    name: "Aventus Man",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    gender: "Male",
    notes: "Bergamot, birch, oakmoss",
    description:
      "Inspired by the great conquerors of history, this fragrance opens with an explosive burst of bergamot and blackcurrant before drifting into a smoky birch and oakmoss heart. Bold, sophisticated, and unmistakably powerful — the scent of ambition.",
    images: ["img/aventus%20man.jpeg"],
  },
  {
    id: "p3",
    name: "Azzaro Chrome",
    priceKes: 2000,
    size: "100ml",
    category: "Fresh",
    gender: "Male",
    notes: "Bergamot, neroli, sea breeze",
    description:
      "Clean, bright, and effortlessly elegant. A sparkling opening of bergamot and neroli gives way to a cool aquatic heart and a dry base of oakmoss and tonka. The quintessential gentleman's fresh — fresh for work, fresh for play.",
    images: ["img/azzaro%20chrome.jpeg"],
  },
  {
    id: "p4",
    name: "Blue Man",
    priceKes: 2000,
    size: "100ml",
    category: "Fresh",
    gender: "Male",
    notes: "Citrus, ginger, cedar",
    description:
      "A crisp, invigorating blue fragrance with a modern edge. Zesty citrus and crushed ginger light up the top, while a clean woody heart of cedar and vetiver grounds the scent. Light enough for daily wear, confident enough for any occasion.",
    images: ["img/blue%20man.jpeg"],
  },
  {
    id: "p5",
    name: "Chocolate",
    priceKes: 2000,
    size: "100ml",
    category: "Gourmand",
    gender: "Unisex",
    notes: "Cocoa, vanilla, brown sugar",
    description:
      "Sinfully indulgent and utterly irresistible. Dark bittersweet cocoa opens like warm ganache, folding into a creamy vanilla and caramelised sugar heart. The base of sandalwood and musk makes it wearable all day — a sweet treat that never gets old.",
    images: ["img/chocolate.jpeg"],
  },
  {
    id: "p6",
    name: "Eros",
    priceKes: 2000,
    size: "100ml",
    category: "Spicy",
    gender: "Male",
    notes: "Mint, apple, vanilla cedar",
    description:
      "Named for the god of love — fresh, sensual, and impossible to ignore. A cool clash of crushed mint and green apple opens boldly, while a heart of tonka bean and geranium adds warmth and sensuality. Cedar and vanilla finish the story with timeless authority.",
    images: ["img/eros.jpeg"],
  },
  {
    id: "p7",
    name: "Guilty Woman",
    priceKes: 2000,
    size: "100ml",
    category: "Floral",
    gender: "Female",
    notes: "Mandarin, peach, white musk",
    description:
      "A playfully seductive floral for the fearlessly feminine. Bright mandarin and juicy peach open the top before flowing into a lush heart of lilac and geranium. The finish is a lingering cloud of soft white musk — light, sensual, free.",
    images: ["img/guilty%20woman.jpeg"],
  },
  {
    id: "p8",
    name: "Interlude Man",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    gender: "Male",
    notes: "Orris, amber, incense",
    description:
      "A contemplative, deeply complex fragrance for those who think between the beats. Bitter orris and oregano clash beautifully in the opening before incense and oud rise through the heart. Amber and sandalwood hold the base in meditative warmth — powerful and poetic.",
    images: ["img/interlude%20man.jpeg"],
  },
  {
    id: "p9",
    name: "Lost Cherry",
    priceKes: 2000,
    size: "100ml",
    category: "Sweet",
    gender: "Unisex",
    notes: "Cherry, almond, tonka",
    description:
      "Dangerously delicious. A burst of tart cherry liqueur smashes into roasted almond and warm clove in the opening — daring and addictive. A creamy tonka bean and Turkish rose heart softens the edge, while vetiver and sandalwood lend a smoky, skin-close depth.",
    images: ["img/lost%20cherry.jpeg"],
  },
  {
    id: "p10",
    name: "Man Extreme",
    priceKes: 2000,
    size: "100ml",
    category: "Spicy",
    gender: "Male",
    notes: "Bergamot, pepper, warm amber",
    description:
      "Pushed to the limit. This intense masculine fragrance opens with sharp bergamot and fiery black pepper, evolving into a bold heart of geranium and earthy vetiver. A base of warm amber and cedarwood makes it long-lasting and memorable — extreme by name, extreme by nature.",
    images: ["img/man%20extreme.jpeg"],
  },
  {
    id: "p11",
    name: "Men in Black",
    priceKes: 2000,
    size: "100ml",
    category: "Woody",
    gender: "Male",
    notes: "Leather, vanilla, dark musk",
    description:
      "Dark, commanding, and magnetic. Black leather and smoky woods form an imposing opening that softens into a heart of iris and spices. Rich vanilla and benzoin anchor the base into a long, enveloping trail — the scent of quiet power and sharp style.",
    images: ["img/men%20in%20black.jpeg"],
  },
  {
    id: "p12",
    name: "Mon Legend",
    priceKes: 2000,
    size: "100ml",
    category: "Fresh",
    gender: "Male",
    notes: "Bergamot, vetiver, white musk",
    description:
      "The everyday legend. Bergamot and aquatic accords lift the opening into a clean, bright freshness. A smooth heart of rosewood and white musks carries through the day, drying down to a crisp vetiver base. Discreet confidence in a bottle.",
    images: ["img/mon%20lengend.jpeg"],
  },
  {
    id: "p13",
    name: "Oud Ispahan",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    gender: "Unisex",
    notes: "Rose, oud, precious woods",
    description:
      "A masterpiece of Oriental perfumery. Saffron and rose absolute open in gilded warmth before a rich heart of rare oud and labdanum takes hold. Patchouli and sandalwood form a velvety base that unfurls on skin for hours — the definition of effortless luxury.",
    images: ["img/oud%20ispahan.jpeg"],
  },
  {
    id: "p14",
    name: "Rouge 540",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    gender: "Unisex",
    notes: "Jasmine, ambergris, cedar",
    description:
      "The scent that stopped the world. An extraordinary floral amber that opens with a rush of jasmine and saffron, blooms into a rich heart of ambergris and fir resin, then settles into a cedar and musk base that becomes your own skin. There is no equivalent.",
    images: ["img/rouge%20540.jpeg"],
  },
  {
    id: "p15",
    name: "Royal Night",
    priceKes: 2000,
    size: "100ml",
    category: "Woody",
    gender: "Unisex",
    notes: "Oud, vanilla, royal amber",
    description:
      "When the palace lights go out and the stars take over — this is what royalty smells like. Smoky oud and saffron command the opening, softened by a heart of aged amber and florals. Vanilla and rich musks close the night in velvet warmth.",
    images: ["img/royal%20night.jpeg"],
  },
  {
    id: "p16",
    name: "Sauvage Elixir",
    priceKes: 2000,
    size: "100ml",
    category: "Spicy",
    gender: "Male",
    notes: "Cardamom, sandalwood, amber",
    description:
      "The most concentrated chapter in the Sauvage saga. Cardamom and nutmeg open with smoky intensity, revealing a heart heavy with earthy licorice and warm lavender. Cold sandalwood and a glowing amber base create an almost magnetic pull — rich, lasting, and undeniably masculine.",
    images: ["img/sauvage%20elixir.jpeg"],
  },
  {
    id: "p17",
    name: "Sauvage",
    priceKes: 2000,
    size: "100ml",
    category: "Fresh",
    gender: "Male",
    notes: "Bergamot, pepper, vetiver",
    description:
      "Raw, wild, and immediately iconic. Fresh Reggio bergamot collides with Sichuan pepper in the opening for an electrifying freshness. Ambroxan and vetiver pull the scent earthward into something vast and sky-wide — as open as a windswept plateau at dawn.",
    images: ["img/sauvage.jpeg"],
  },
  {
    id: "p18",
    name: "Scandal Man",
    priceKes: 2000,
    size: "100ml",
    category: "Sweet",
    gender: "Male",
    notes: "Honey, grapefruit, leather",
    description:
      "Provocative and polarising — exactly as intended. Juicy grapefruit zests up the opening while a unique honey and sage accord creates an addictive, edgy heart. Leather and patchouli give the base real character. It does not blend in. It never will.",
    images: ["img/scandal%20man.jpeg"],
  },
  {
    id: "p19",
    name: "Splendid Vanilla",
    priceKes: 2000,
    size: "100ml",
    category: "Gourmand",
    gender: "Unisex",
    notes: "Vanilla, tonka, warm woods",
    description:
      "Vanilla elevated to its highest form. Warm, creamy Madagascar vanilla opens without any sweetness overdrive, balanced beautifully by tonka bean and soft iris. A base of smooth woods and musks makes this the most wearable vanilla you'll ever own — splendid is the only word.",
    images: ["img/splendid%20vanilla.jpeg"],
  },
  {
    id: "p20",
    name: "Stronger with You Oud",
    priceKes: 2000,
    size: "100ml",
    category: "Luxury",
    gender: "Male",
    notes: "Cardamom, oud, caramel",
    description:
      "A bolder, darker chapter of an iconic love story. Spiced cardamom and chestnut open the top with warmth, while a Middle Eastern oud heart adds depth and gravitas. Caramelised sugar and vanilla in the base make it intimate and tender — strength wrapped in sweetness.",
    images: ["img/stronger%20with%20you%20oud.jpeg"],
  },
  {
    id: "p21",
    name: "Tere DH",
    priceKes: 2000,
    size: "100ml",
    category: "Woody",
    gender: "Male",
    notes: "Cedarwood, oud, dry musk",
    description:
      "A no-nonsense woody powerhouse built for the modern man. Dry cedarwood and light bergamot open cleanly before oud and vetiver take over with quiet authority. A mineral musk base keeps it skin-close and long-lasting — a wardrobe staple.",
    images: ["img/tere%20dh.jpeg"],
  },
  {
    id: "p22",
    name: "Tobacco Vanilla",
    priceKes: 2000,
    size: "100ml",
    category: "Gourmand",
    gender: "Unisex",
    notes: "Tobacco, vanilla, brown sugar",
    description:
      "Dark, sweet, and utterly irresistible — the cult classic reimagined. Aromatic tobacco leaf and warm spices crack open unexpectedly sweet in the opening, flowing into a rich vanilla absoluté laced with brown sugar and rum. The dry-down of benzoin and sequoia is pure indulgence.",
    images: ["img/tobacco%20vanilla.jpeg"],
  },
  {
    id: "p23",
    name: "Vanilla",
    priceKes: 2000,
    size: "100ml",
    category: "Sweet",
    gender: "Female",
    notes: "Vanilla, amber, soft musk",
    description:
      "The purest expression of vanilla — stripped of everything it doesn't need. Warm, creamy, and just the right amount of sweet. Soft amber adds a subtle glow to the heart, while sandalwood and clean musk in the base keep it grounded and wearable. Simple. Perfect. Timeless.",
    images: ["img/vanilla.jpeg"],
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
