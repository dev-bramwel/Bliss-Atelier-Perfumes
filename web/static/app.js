// --------------------
// DOM (index page)
// --------------------
const productGrid = document.getElementById("productGrid");
const resultsCount = document.getElementById("resultsCount");

const searchInput = document.getElementById("searchInput");
const categorySelect = document.getElementById("categorySelect");
const genderSelect = document.getElementById("genderSelect");

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
  if (!categorySelect) return;
  const prev = categorySelect.value;
  categorySelect.innerHTML = desktopOptions;
  categorySelect.value = categories.includes(prev) ? prev : "all";
}

// --------------------
// RENDER: Products
// --------------------
function getFilters() {
  return {
    search: (searchInput?.value ?? "").trim(),
    category: categorySelect?.value ?? "all",
    gender: genderSelect?.value ?? "all",
  };
}

function renderProducts() {
  if (!PRODUCTS || PRODUCTS.length === 0) {
    resultsCount.textContent = "0";
    productGrid.textContent =
      catalogState === "loading"
        ? "Loading collection..."
        : "Collection unavailable. Please try again shortly.";
    return;
  }
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
      const eName = escapeHTML(p.name);
      const eGender = escapeHTML(p.gender ?? "Unisex");
      const eCategory = escapeHTML(p.category);
      const eSize = escapeHTML(p.size);
      const eNotes = escapeHTML(p.notes);
      const eThumb = escapeHTML(thumb);
      const canBuy = catalogFresh() && p.available;
      const btnText = !catalogFresh()
        ? "Prices unavailable"
        : p.available
          ? "Add to cart"
          : "Currently unavailable";
      return `
    <div class="collection-card flex flex-col">
      <button type="button" class="product-card-img" data-open-product="${p.id}" aria-label="View details for ${eName}">
        <img src="${eThumb}" alt=""
             onerror="this.onerror=null;this.classList.add('img-broken')" loading="lazy">
        <div class="product-card-img-placeholder"><span>${eName.charAt(0)}</span></div>
        <div class="product-card-img-overlay">
          <span class="product-card-view-btn">View Details</span>
        </div>
      </button>
      <div class="collection-card-content flex flex-col flex-1">
        <div class="flex items-start justify-between gap-3">
          <div>
            <div class="collection-card-category">${eGender} &bull; ${eCategory}</div>
            <h3 class="collection-card-name">${eName}</h3>
            <p class="collection-card-notes">${eSize} &bull; ${eNotes}</p>
            <p class="stock-badge" data-low="${p.stock > 0 && p.stock <= 5}">${!p.available ? (p.stock === 0 ? "Sold Out" : "Currently unavailable") : p.stock <= 5 ? "Only " + p.stock + " left in stock" : "In Stock"}</p>
          </div>
          <div class="collection-card-price-wrap">
            <div class="collection-card-price">KES ${formatKes(p.priceKes)}</div>
          </div>
        </div>
        <div class="collection-card-actions">
          <button class="collection-add gold-btn" data-add="${p.id}" ${canBuy ? "" : "disabled"}>
            ${btnText}
          </button>
          <a class="collection-whatsapp"
             href="${wa}" target="_blank" rel="noreferrer">
            WhatsApp
          </a>
        </div>
      </div>
    </div>`;
    })
    .join("");

  bindCardEvents();
}

function bindCardEvents() {
  document.querySelectorAll("[data-add]").forEach((btn) => {
    btn.onclick = () => {
      addToCart(btn.dataset.add);
      updateCartBadges();
    };
  });

  document.querySelectorAll("[data-open-product]").forEach((el) => {
    el.onclick = () => openProductModal(el.dataset.openProduct, el);
  });
}

// --------------------
// PRODUCT DETAIL MODAL
// --------------------
let _modal = null;
let _modalOpener = null;
let _modalImages = [];
let _modalImageIdx = 0;

function getOrCreateModal() {
  if (_modal) return _modal;

  let overlay = document.getElementById("productModal");
  if (!overlay) {
    overlay = document.createElement("div");
    overlay.id = "productModal";
    overlay.className = "product-modal-overlay";
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.setAttribute("aria-labelledby", "modalName");
    overlay.innerHTML = `
    <div class="product-modal-panel">
      <button type="button" class="product-modal-close" id="modalClose" aria-label="Close product details">
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
  }

  _modal = overlay;

  if (!overlay.dataset.initialized) {
    overlay.dataset.initialized = "true";

    overlay.addEventListener("click", (e) => {
      if (e.target === overlay) closeProductModal();
    });
    document
      .getElementById("modalClose")
      ?.addEventListener("click", closeProductModal);
    document
      .getElementById("modalPrev")
      ?.addEventListener("click", () => shiftModalImage(-1));
    document
      .getElementById("modalNext")
      ?.addEventListener("click", () => shiftModalImage(1));
    document
      .getElementById("modalAddToCart")
      ?.addEventListener("click", () => {
        const id = overlay.dataset.productId;
        if (id) {
          addToCart(id);
          updateCartBadges();
        }
      });

    document.addEventListener("keydown", (e) => {
      if (!overlay.classList.contains("open")) return;
      if (e.key === "Escape") {
        e.preventDefault();
        closeProductModal();
        return;
      }
      if (e.key === "Tab") {
        const focusable = Array.from(overlay.querySelectorAll(
          'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        )).filter((el) => el.getClientRects().length > 0);
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (!first) return;
        if (e.shiftKey && (document.activeElement === first || !overlay.contains(document.activeElement))) {
          e.preventDefault();
          last?.focus?.();
        } else if (!e.shiftKey && (document.activeElement === last || !overlay.contains(document.activeElement))) {
          e.preventDefault();
          first?.focus?.();
        }
      }
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
  }

  return overlay;
}

function openProductModal(id, opener = null) {
  const p = getProduct(id);
  if (!p) return;

  const modal = getOrCreateModal();
  _modalOpener = opener || document.activeElement;
  modal.dataset.productId = id;
  _modalImages = p.images || [];
  _modalImageIdx = 0;

  const canBuy = catalogFresh() && p.available;
  document.getElementById("modalBadge").textContent =
    `${p.gender ?? "Unisex"} \u2022 ${p.category}`;
  document.getElementById("modalName").textContent = p.name;
  document.getElementById("modalAddToCart").disabled = !canBuy;
  document.getElementById("modalAddToCart").textContent = !catalogFresh()
    ? "Prices unavailable"
    : p.available
      ? "Add to Cart"
      : "Currently unavailable";
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
  document.getElementById("modalClose")?.focus?.();
}

function closeProductModal() {
  if (_modal) {
    _modal.classList.remove("open");
    document.body.style.overflow = "";
    _modalOpener?.focus?.();
    _modalOpener = null;
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
          <img src="${escapeHTML(src)}" alt="Thumbnail ${i + 1}" onerror="this.onerror=null;this.classList.add('img-broken')" loading="lazy">
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
    window.location.href = "/cart";
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
searchInput?.addEventListener("input", renderProducts);
categorySelect?.addEventListener("change", renderProducts);
genderSelect?.addEventListener("change", (e) => {
  updateCategorySelectOptions(e.target.value);
  renderProducts();
});

// --------------------
// INIT
// --------------------
renderFilterOptions();
bindCardEvents();
if (productGrid && productGrid.children.length === 0) {
  renderProducts();
}
updateCartBadges();
updateBackToTopVisibility();
getOrCreateModal();

let catalogFiltersInitialized = PRODUCTS.length > 0;
document.addEventListener("catalogUpdated", () => {
  if (!catalogFiltersInitialized && PRODUCTS.length > 0) {
    renderFilterOptions();
    catalogFiltersInitialized = true;
  }
  renderProducts();
});
