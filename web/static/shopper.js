(() => {
  if (/^\/(admin(?:\/|$)|dashboard)/.test(location.pathname)) return;
  const $ = (id) => document.getElementById(id);
  let user = null,
    csrf = "",
    offset = 0;
  function profile() {
    if (!$("shopperSignIn")) return;
    $("shopperSignIn").hidden = !!user;
    $("shopperName").hidden = !user;
    $("shopperName").textContent = user?.name || "";
    $("shopperLogout").hidden = !user;
    const image = $("shopperAvatar");
    image.hidden = true;
    if (user?.picture_url) {
      try {
        const url = new URL(user.picture_url);
        if (url.protocol === "https:") {
          image.src = url.href;
          image.hidden = false;
        }
      } catch {}
    }
    const active = read("bliss_active_order");
    $("resumeOrder").hidden = !active?.id;
    if (active?.id)
      $("resumeOrder").href = "/order/" + encodeURIComponent(active.id);
  }
  function read(key) {
    try {
      return JSON.parse(localStorage.getItem(key) || "null");
    } catch {
      return null;
    }
  }
  function write(key, value) {
    try {
      localStorage.setItem(key, JSON.stringify(value));
    } catch {
      throw new Error(
        "Browser storage is unavailable. Enable site storage before placing an order.",
      );
    }
  }
  async function request(path, options = {}) {
    if (window.BlissConnection.offline)
      throw new Error("Connection paused. Your inputs are preserved.");
    const headers = { ...options.headers };
    if (options.method && options.method !== "GET") {
      headers["Content-Type"] = "application/json";
      if (csrf) headers["X-CSRF-Token"] = csrf;
    }
    let response;
    try {
      response = await fetch("/api" + path, {
        ...options,
        headers,
        credentials: "same-origin",
        cache: "no-store",
        signal: AbortSignal.timeout(15000),
      });
    } catch (error) {
      window.BlissConnection.dropped();
      throw new Error(
        "Connection interrupted. Check this order before trying again.",
      );
    }
    const date = Date.parse(response.headers.get("Date") || "");
    if (Number.isFinite(date)) offset = date - Date.now();
    if (response.status >= 500) {
      window.BlissConnection.dropped();
      throw new Error(
        "The atelier is temporarily unavailable. Your inputs are preserved.",
      );
    }
    let data;
    try {
      data = await response.json();
    } catch {
      throw new Error(
        "The atelier returned an unreadable response. Please check your order before retrying.",
      );
    }
    if (!response.ok) {
      if (response.status === 401) {
        user = null;
        csrf = "";
        profile();
      }
      const error = new Error(
        response.status === 401
          ? "Sign in again or open this order in the browser where you placed it."
          : response.status === 403
            ? "You do not have access to this order or your session changed. Please sign in again."
            : data.error || "Could not complete this request.",
      );
      error.status = response.status;
      throw error;
    }
    return data;
  }
  async function auth() {
    try {
      const data = await request("/auth/me");
      user = data.user;
      csrf = data.csrf_token || "";
      $("shopperAuthMessage").textContent = "";
    } catch (error) {
      if (!error.status || error.status !== 401)
        $("shopperAuthMessage").textContent = error.message;
    }
    profile();
    window.dispatchEvent(new Event("bliss:identity"));
  }
  const ready = auth();
  $("shopperLogout")?.addEventListener("click", async () => {
    try {
      await request("/auth/logout", { method: "POST", body: "{}" });
      user = null;
      csrf = "";
      profile();
      window.dispatchEvent(new Event("bliss:identity"));
    } catch (error) {
      $("shopperAuthMessage").textContent = error.message;
    }
  });
  window.addEventListener("bliss:connection", () => {
    if (!window.BlissConnection.offline) auth();
  });
  window.addEventListener("storage", profile);
  window.BlissShopper = {
    request,
    read,
    write,
    ready,
    profile,
    get user() {
      return user;
    },
    get now() {
      return Date.now() + offset;
    },
    uuid: () => crypto.randomUUID(),
  };
})();
