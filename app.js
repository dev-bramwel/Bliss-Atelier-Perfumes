// --------------------
// DOM (index page)
// --------------------
const productGrid = document.getElementById("productGrid");
const resultsCount = document.getElementById("resultsCount");

const searchInput = document.getElementById("searchInput");
const categorySelect = document.getElementById("categorySelect");
const searchInputMobile = document.getElementById("searchInputMobile");
const categorySelectMobile = document.getElementById("categorySelectMobile");
const genderSelect = document.getElementById("genderSelect");
const genderSelectMobile = document.getElementById("genderSelectMobile");

const whatsappLink = document.getElementById("whatsappLink");
const backToTopBtn = document.getElementById("backToTopBtn");

// --------------------
// RENDER: Filter selects
// --------------------
function renderFilterOptions() {
  const genderOptions = [
    "all",
    ...Array.from(new Set(PRODUCTS.map((p) => p.gender || "Unisex"))),
  ];

  if (genderSelect) {
    genderSelect.innerHTML = genderOptions
      .map(
        (g) =>
          `<option value="${g}">${g === "all" ? "All genders" : g}</option>`,
      )
      .join("");
    genderSelect.value = "all";
  }

  if (genderSelectMobile) {
    genderSelectMobile.innerHTML = genderOptions
      .map((g) => `<option value="${g}">${g === "all" ? "All" : g}</option>`)
      .join("");
    genderSelectMobile.value = "all";
  }

  updateCategorySelectOptions("all");
}

function updateCategorySelectOptions(selectedGender = "all") {
  const filtered = PRODUCTS.filter(
    (p) =>
      selectedGender === "all" || (p.gender || "Unisex") === selectedGender,
  );
  const categories = [
    "all",
    ...Array.from(new Set(filtered.map((p) => p.category))),
  ];

  const desktopOptions = categories
    .map(
      (c) =>
        `<option value="${c}">${c === "all" ? "All categories" : c}</option>`,
    )
    .join("");
  const mobileOptions = categories
    .map((c) => `<option value="${c}">${c === "all" ? "All" : c}</option>`)
    .join("");

  [categorySelect, categorySelectMobile].forEach((sel, idx) => {
    if (!sel) return;
    const prev = sel.value;
    sel.innerHTML = idx === 0 ? desktopOptions : mobileOptions;
    sel.value = categories.includes(prev) ? prev : "all";
  });
}

// --------------------
// RENDER: Products
// --------------------
function getFilters() {
  const s = (searchInput?.value ?? "").trim();
  const sm = (searchInputMobile?.value ?? "").trim();
  const search = s.length ? s : sm;

  const g = genderSelect?.value ?? "all";
  const gm = genderSelectMobile?.value ?? "all";
  const gender = g !== "all" ? g : gm !== "all" ? gm : "all";

  const c = categorySelect?.value ?? "all";
  const cm = categorySelectMobile?.value ?? "all";
  const category = c !== "all" ? c : cm !== "all" ? cm : "all";

  return { search, category, gender };
}

function renderProducts() {
  const { search, category, gender } = getFilters();
  let filtered = PRODUCTS.slice();

  if (gender !== "all") {
    filtered = filtered.filter((p) => (p.gender || "Unisex") === gender);
  }
  if (category !== "all") {
    filtered = filtered.filter((p) => p.category === category);
  }
  if (search.length) {
    const q = search.toLowerCase();
    filtered = filtered.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.notes.toLowerCase().includes(q) ||
        p.category.toLowerCase().includes(q),
    );
  }

  resultsCount.textContent = String(filtered.length);

  productGrid.innerHTML = filtered
    .map(
      (p) => `
    <div class="glass-panel rounded-3xl p-5 border border-white/10">
      <div class="flex items-start justify-between gap-3">
        <div>
          <div class="text-xs uppercase tracking-[0.35em] text-[var(--gold-soft)]">${p.gender ?? "Unisex"} &bull; ${p.category}</div>
          <h3 class="text-xl font-semibold text-white">${p.name}</h3>
          <p class="text-sm text-gray-400">${p.size} &bull; ${p.notes}</p>
        </div>
        <div class="text-right shrink-0">
          <div class="font-semibold text-lg text-[var(--gold-soft)]">KES ${formatKes(p.priceKes)}</div>
        </div>
      </div>

      <div class="mt-5 flex flex-col sm:flex-row gap-2">
        <button class="w-full sm:flex-1 py-2.5 rounded-2xl gold-btn text-sm" data-add="${p.id}">
          Add to cart
        </button>
        <a class="w-full sm:w-auto py-2.5 px-4 rounded-2xl border border-white/20 text-sm text-gray-100 text-center hover:bg-white/10"
           href="${buildWhatsAppMessage([`Item: ${p.name} (${p.size})`, `Price: KES ${p.priceKes}`])}"
           target="_blank" rel="noreferrer">
          WhatsApp
        </a>
      </div>
    </div>
  `,
    )
    .join("");

  document.querySelectorAll("[data-add]").forEach((btn) => {
    btn.addEventListener("click", () => {
      addToCart(btn.dataset.add);
      updateCartBadges();
    });
  });
}

// --------------------
// CART BADGE (index nav)
// --------------------
function updateCartBadges() {
  const count = cartCount();
  document.querySelectorAll("[data-cart-count]").forEach((el) => {
    el.textContent = String(count);
  });

  // Update WhatsApp link in footer
  if (whatsappLink) {
    whatsappLink.href =
      count === 0
        ? `https://wa.me/${SHOP_WHATSAPP_NUMBER}?text=${encodeURIComponent("Hi, I want to ask about your perfumes.")}`
        : buildWhatsAppMessage(["My location:", "Preferred delivery time:"]);
  }
}

// --------------------
// CART NAVIGATION
// --------------------
document.querySelectorAll("[data-open-cart]").forEach((btn) => {
  btn.addEventListener("click", () => {
    window.location.href = "cart.html";
  });
});

// --------------------
// BACK TO TOP
// --------------------
function updateBackToTopVisibility() {
  if (!backToTopBtn) return;
  if (window.scrollY > 220) {
    backToTopBtn.classList.add("visible");
  } else {
    backToTopBtn.classList.remove("visible");
  }
}

backToTopBtn?.addEventListener("click", () =>
  window.scrollTo({ top: 0, behavior: "smooth" }),
);
window.addEventListener("scroll", updateBackToTopVisibility, { passive: true });

// --------------------
// FILTER EVENTS
// --------------------
[searchInput, searchInputMobile].forEach((inp) =>
  inp?.addEventListener("input", renderProducts),
);
[categorySelect, categorySelectMobile].forEach((sel) =>
  sel?.addEventListener("change", renderProducts),
);
[genderSelect, genderSelectMobile].forEach((sel) =>
  sel?.addEventListener("change", (e) => {
    const value = e.target.value;
    if (sel === genderSelect && genderSelectMobile)
      genderSelectMobile.value = value;
    if (sel === genderSelectMobile && genderSelect) genderSelect.value = value;
    updateCategorySelectOptions(value);
    renderProducts();
  }),
);

// --------------------
// INIT
// --------------------
renderFilterOptions();
renderProducts();
updateCartBadges();
updateBackToTopVisibility();
