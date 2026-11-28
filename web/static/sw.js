const CACHE_PREFIX = "bliss-pwa-";
const CACHE_NAME = `${CACHE_PREFIX}v1`;
const OFFLINE_URL = "/offline.html";

self.addEventListener("install", (event) => {
    event.waitUntil(
        caches.open(CACHE_NAME)
            .then((cache) => cache.add(OFFLINE_URL))
            .then(() => self.skipWaiting())
    );
});

self.addEventListener("activate", (event) => {
    event.waitUntil(
        caches.keys()
            .then((keys) => Promise.all(
                keys.filter((key) =>
                    key.startsWith(CACHE_PREFIX) &&
                    key !== CACHE_NAME
                ).map((key) => caches.delete(key))
            ))
            .then(() => self.clients.claim())
    );
});

self.addEventListener("fetch", (event) => {
    const request = event.request;
    const url = new URL(request.url);

    if (request.method !== "GET" ||
        url.origin !== self.location.origin) {
        return;
    }

    if (request.mode === "navigate") {
        event.respondWith(
            fetch(request).catch(async () =>
                (await caches.match(OFFLINE_URL)) ||
                new Response("You are offline. Please reconnect and retry.", {
                    status: 503,
                    headers: { "Content-Type": "text/plain; charset=utf-8" }
                })
            )
        );
        return;
    }

    if (!url.pathname.startsWith("/static/") &&
        !url.pathname.startsWith("/img/")) {
        return;
    }

    event.respondWith((async () => {
        const cache = await caches.open(CACHE_NAME);

        try {
            const response = await fetch(request);
            const policy = response.headers.get("Cache-Control") || "";

            if (response.ok && !/no-store|private/i.test(policy)) {
                await cache.put(request, response.clone()).catch(() => {});
            }

            return response;
        } catch {
            return (await cache.match(request)) || Response.error();
        }
    })());
});

self.addEventListener("push", (event) => {
    let payload = {};

    if (event.data) {
        try {
            payload = event.data.json() || {};
        } catch {
            payload = { body: event.data.text() };
        }
    }

    event.waitUntil(
        self.registration.showNotification(
            payload.title || "Bliss Atelier Perfumes",
            {
                body: payload.body || "You have a new update.",
                icon: "/static/icons/icon-192.png",
                badge: "/static/icons/icon-192.png",
                data: { url: payload.url || "/" }
            }
        )
    );
});

self.addEventListener("notificationclick", (event) => {
    event.notification.close();

    let target = new URL("/", self.location.origin);

    try {
        const url = new URL(
            event.notification.data?.url || "/",
            self.location.origin
        );

        if (url.origin === self.location.origin) {
            target = url;
        }
    } catch {
        // Fall back to the landing page.
    }

    event.waitUntil((async () => {
        const clients = await self.clients.matchAll({
            type: "window",
            includeUncontrolled: true
        });

        for (const client of clients) {
            if (client.url === target.href) {
                return client.focus();
            }
        }

        return self.clients.openWindow(target.href);
    })());
});
