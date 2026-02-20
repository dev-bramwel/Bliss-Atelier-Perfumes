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
    .map((p) => {
      const thumb = (p.images || [])[0] || "";
      const wa = buildWhatsAppMessage([
        `Item: ${p.name} (${p.size})`,
        `Price: KES ${p.priceKes}`,
      ]);
      return `
    <div class="glass-panel rounded-3xl overflow-hidden border border-white/10 flex flex-col">
      <div class="product-card-img" data-open-product="${p.id}">
        <img src="${thumb}" alt="${p.name}"
             onerror="this.onerror=null;this.classList.add('img-broken')" loading="lazy">
        <div class="product-card-img-placeholder"><span>${p.name.charAt(0)}</span></div>
        <div class="product-card-img-overlay">
          <span class="product-card-view-btn">View Details</span>
        </div>
      </div>
      <div class="p-5 flex flex-col flex-1">
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
             href="${wa}" target="_blank" rel="noreferrer">
            WhatsApp
          </a>
        </div>
      </div>
    </div>`;
    })
    .join("");

  document.querySelectorAll("[data-add]").forEach((btn) => {
    btn.addEventListener("click", () => {
      addToCart(btn.dataset.add);
      updateCartBadges();
    });
  });

  document.querySelectorAll("[data-open-product]").forEach((el) => {
    el.addEventListener("click", () =>
      openProductModal(el.dataset.openProduct),
    );
  });
}

// --------------------
// PRODUCT DETAIL MODAL
// --------------------
let _modal = null;
let _modalImages = [];
let _modalImageIdx = 0;

function getOrCreateModal() {
  if (_modal) return _modal;

  const overlay = document.createElement("div");
  overlay.id = "productModal";
  overlay.className = "product-modal-overlay";
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  overlay.innerHTML = `
    <div class="product-modal-panel">
      <button class="product-modal-close" id="modalClose" aria-label="Close">
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="18" height="18" fill="currentColor">
          <path d="M5.28 4.22a.75.75 0 0 0-1.06 1.06L10.94 12l-6.72 6.72a.75.75 0 1 0 1.06 1.06L12 13.06l6.72 6.72a.75.75 0 1 0 1.06-1.06L13.06 12l6.72-6.72a.75.75 0 0 0-1.06-1.06L12 10.94 5.28 4.22Z"/>
        </svg>
      </button>
      <div class="product-modal-inner">
        <div class="product-modal-images">
          <div class="product-modal-main-img-wrap" id="modalImgWrap">
            <img id="modalMainImg" src="" alt="" class="product-modal-main-img">
            <div class="product-modal-img-placeholder"><span id="modalImgInitial"></span></div>
            <button class="modal-nav-btn modal-nav-prev" id="modalPrev" aria-label="Previous image">
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="18" height="18" fill="currentColor"><path d="M14.47 5.47a.75.75 0 0 1 0 1.06L8.81 12l5.66 5.47a.75.75 0 1 1-1.04 1.08l-6.25-6a.75.75 0 0 1 0-1.08l6.25-6a.75.75 0 0 1 1.04.02Z"/></svg>
            </button>
            <button class="modal-nav-btn modal-nav-next" id="modalNext" aria-label="Next image">
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="18" height="18" fill="currentColor"><path d="M9.53 5.47a.75.75 0 0 0 0 1.06L15.19 12l-5.66 5.47a.75.75 0 1 0 1.04 1.08l6.25-6a.75.75 0 0 0 0-1.08l-6.25-6a.75.75 0 0 0-1.04.02Z"/></svg>
            </button>
          </div>
          <div id="modalThumbs" class="product-modal-thumbs"></div>
        </div>
        <div class="product-modal-details">
          <div class="product-modal-badge" id="modalBadge"></div>
          <h2 class="product-modal-name" id="modalName"></h2>
          <div class="product-modal-price" id="modalPrice"></div>
          <div class="product-modal-meta" id="modalMeta"></div>
          <p class="product-modal-description" id="modalDescription"></p>
          <div class="product-modal-actions">
            <button id="modalAddToCart" class="gold-btn py-3 rounded-2xl font-medium text-sm w-full">Add to Cart</button>
            <a id="modalWhatsApp" href="#" target="_blank" rel="noreferrer"
               class="gold-outline-btn py-3 rounded-2xl text-sm font-semibold text-center w-full block">WhatsApp Order</a>
          </div>
        </div>
      </div>
    </div>
  `;

  document.body.appendChild(overlay);

  overlay.addEventListener("click", (e) => {
    if (e.target === overlay) closeProductModal();
  });
  document
    .getElementById("modalClose")
    .addEventListener("click", closeProductModal);
  document
    .getElementById("modalPrev")
    .addEventListener("click", () => shiftModalImage(-1));
  document
    .getElementById("modalNext")
    .addEventListener("click", () => shiftModalImage(1));
  document.getElementById("modalAddToCart").addEventListener("click", () => {
    const id = overlay.dataset.productId;
    if (id) {
      addToCart(id);
      updateCartBadges();
    }
  });

  document.addEventListener("keydown", (e) => {
    if (!overlay.classList.contains("open")) return;
    if (e.key === "Escape") closeProductModal();
    if (e.key === "ArrowRight") shiftModalImage(1);
    if (e.key === "ArrowLeft") shiftModalImage(-1);
  });

  // Touch swipe support
  let _touchX = null;
  overlay.addEventListener(
    "touchstart",
    (e) => {
      _touchX = e.touches[0].clientX;
    },
    { passive: true },
  );
  overlay.addEventListener(
    "touchend",
    (e) => {
      if (_touchX === null) return;
      const dx = e.changedTouches[0].clientX - _touchX;
      if (Math.abs(dx) > 50) shiftModalImage(dx < 0 ? 1 : -1);
      _touchX = null;
    },
    { passive: true },
  );

  _modal = overlay;
  return overlay;
}

function openProductModal(id) {
  const p = getProduct(id);
  if (!p) return;

  const modal = getOrCreateModal();
  modal.dataset.productId = id;
  _modalImages = p.images || [];
  _modalImageIdx = 0;

  document.getElementById("modalBadge").textContent =
    `${p.gender ?? "Unisex"} \u2022 ${p.category}`;
  document.getElementById("modalName").textContent = p.name;
  document.getElementById("modalPrice").textContent =
    `KES ${formatKes(p.priceKes)}`;
  document.getElementById("modalMeta").textContent =
    `${p.size} \u2022 ${p.notes}`;
  document.getElementById("modalDescription").textContent = p.description || "";
  document.getElementById("modalImgInitial").textContent = p.name.charAt(0);
  document.getElementById("modalWhatsApp").href = buildWhatsAppMessage([
    `Item: ${p.name} (${p.size})`,
    `Price: KES ${p.priceKes}`,
  ]);

  const navBtns = modal.querySelectorAll(".modal-nav-btn");
  navBtns.forEach(
    (b) => (b.style.display = _modalImages.length > 1 ? "" : "none"),
  );

  renderModalImages();
  modal.classList.add("open");
  document.body.style.overflow = "hidden";
}

function closeProductModal() {
  if (_modal) {
    _modal.classList.remove("open");
    document.body.style.overflow = "";
  }
}

function renderModalImages() {
  const mainImg = document.getElementById("modalMainImg");
  const wrap = document.getElementById("modalImgWrap");
  const thumbsEl = document.getElementById("modalThumbs");

  if (_modalImages.length === 0) {
    mainImg.src = "";
    wrap.classList.add("no-img");
    thumbsEl.innerHTML = "";
    return;
  }

  wrap.classList.remove("no-img");
  mainImg.classList.remove("img-broken");
  mainImg.src = _modalImages[_modalImageIdx];
  mainImg.alt = `Image ${_modalImageIdx + 1}`;
  mainImg.onerror = () => {
    mainImg.classList.add("img-broken");
    wrap.classList.add("no-img");
  };
  mainImg.onload = () => {
    mainImg.classList.remove("img-broken");
    wrap.classList.remove("no-img");
  };

  thumbsEl.innerHTML = _modalImages
    .map(
      (src, i) =>
        `<button class="product-thumb-btn${i === _modalImageIdx ? " active" : ""}" data-tidx="${i}">
          <img src="${src}" alt="Thumbnail ${i + 1}" onerror="this.onerror=null;this.classList.add('img-broken')" loading="lazy">
        </button>`,
    )
    .join("");

  thumbsEl.querySelectorAll("[data-tidx]").forEach((btn) => {
    btn.addEventListener("click", () => {
      _modalImageIdx = parseInt(btn.dataset.tidx, 10);
      renderModalImages();
    });
  });
}

function shiftModalImage(dir) {
  if (_modalImages.length < 2) return;
  _modalImageIdx =
    (_modalImageIdx + dir + _modalImages.length) % _modalImages.length;
  renderModalImages();
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
