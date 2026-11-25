if ("serviceWorker" in navigator) {
    window.addEventListener("load", async () => {
        try {
            await navigator.serviceWorker.register("/sw.js", {
                scope: "/"
            });
        } catch (error) {
            console.error("Service worker registration failed:", error);
        }
    });
}

const installButton = document.getElementById("pwaInstallButton");
let deferredPrompt = null;

window.addEventListener("beforeinstallprompt", (event) => {
    if (!installButton) return;

    event.preventDefault();
    deferredPrompt = event;
    installButton.hidden = false;
});

installButton?.addEventListener("click", async () => {
    if (!deferredPrompt) return;

    const prompt = deferredPrompt;
    deferredPrompt = null;
    installButton.hidden = true;

    try {
        await prompt.prompt();
        await prompt.userChoice;
    } catch (error) {
        console.error("Installation failed:", error);
    }
});

window.addEventListener("appinstalled", () => {
    deferredPrompt = null;
    if (installButton) installButton.hidden = true;
});

const iosBanner = document.getElementById("pwaIOSBanner");
const iosDismiss = document.getElementById("pwaIOSDismiss");

const isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

const isSafari = /Safari/.test(navigator.userAgent) &&
    !/CriOS|FxiOS|EdgiOS|OPiOS/.test(navigator.userAgent);

const isStandalone = window.matchMedia("(display-mode: standalone)").matches ||
    navigator.standalone === true;

let iosDismissed = false;

try {
    iosDismissed = localStorage.getItem("pwa-ios-dismissed") === "true";
} catch {}

if (iosBanner && isIOS && isSafari && !isStandalone && !iosDismissed) {
    iosBanner.hidden = false;
}

iosDismiss?.addEventListener("click", () => {
    iosBanner.hidden = true;

    try {
        localStorage.setItem("pwa-ios-dismissed", "true");
    } catch {}
});

async function subscribeToPush(vapidPublicKey) {
    if (!("serviceWorker" in navigator) ||
        !("PushManager" in window)) {
        throw new Error("Web Push is not supported.");
    }

    if (!vapidPublicKey) {
        throw new Error("VAPID public key is required.");
    }

    const padding = "=".repeat((4 - vapidPublicKey.length % 4) % 4);
    const base64 = (vapidPublicKey + padding)
        .replace(/-/g, "+")
        .replace(/_/g, "/");

    const applicationServerKey = Uint8Array.from(
        atob(base64), (char) => char.charCodeAt(0)
    );

    const registration = await navigator.serviceWorker.ready;

    let subscription = await registration.pushManager.getSubscription();

    if (!subscription) {
        subscription = await registration.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey
        });
    }

    const response = await fetch("/api/push/subscribe", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(subscription)
    });

    if (!response.ok) {
        throw new Error(`Push subscription failed: ${response.status}`);
    }

    return subscription;
}
