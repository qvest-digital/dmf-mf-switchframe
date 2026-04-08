// Service worker for Switchframe SPA resilience.
// Caches the app shell so the page loads even if the engine is unreachable.

// Cache version derived from the HTML content hash. On each deploy, the SW
// install handler fetches '/' and hashes it — different HTML (which references
// new /_app/ bundles) produces a different cache name, automatically evicting
// stale assets from prior deploys. This prevents serving mismatched cached
// assets when dual engines are on different deploy versions.
const CACHE_VERSION_PREFIX = 'switchframe-app-';
let CACHE_NAME = 'switchframe-app-v1'; // updated dynamically during install

// App shell assets to precache on install.
// SvelteKit generates hashed filenames, so we cache on first fetch.
const APP_SHELL_PATTERNS = [
    /^\/$/, // index.html
    /\/_app\//, // SvelteKit app assets
    /\.js$/, // JavaScript bundles
    /\.css$/, // Stylesheets
    /\.wasm$/, // WASM modules (if any)
];

function isAppShellRequest(url) {
    const path = new URL(url).pathname;
    return APP_SHELL_PATTERNS.some(pattern => pattern.test(path));
}

function isNavigationRequest(request) {
    return request.mode === 'navigate';
}

function isApiRequest(url) {
    const path = new URL(url).pathname;
    return path.startsWith('/api/') || path.startsWith('/prism/');
}

/** Simple hash of a string for cache versioning. */
async function hashString(str) {
    const data = new TextEncoder().encode(str);
    const buf = await crypto.subtle.digest('SHA-256', data);
    const arr = Array.from(new Uint8Array(buf));
    return arr.slice(0, 8).map(b => b.toString(16).padStart(2, '0')).join('');
}

self.addEventListener('install', (event) => {
    // Precache all SvelteKit route nodes and entry points so the app works
    // even if the serving engine dies mid-session (code-split chunks would
    // fail to load from the dead origin without this).
    event.waitUntil(
        (async () => {
            try {
                // Fetch the page HTML to discover script references
                const htmlRes = await fetch('/');
                if (htmlRes.ok) {
                    const html = await htmlRes.text();
                    // Derive cache name from HTML content hash so different
                    // deploys get separate caches (prevents stale asset serving).
                    const hash = await hashString(html);
                    CACHE_NAME = CACHE_VERSION_PREFIX + hash;

                    const cache = await caches.open(CACHE_NAME);
                    await cache.put('/', new Response(html, { headers: htmlRes.headers }));
                    // Extract all /_app/ script and link references from the HTML
                    const assetPattern = /\/_app\/[^"'\s)]+/g;
                    const assets = [...new Set(html.match(assetPattern) || [])];
                    // Prefetch all discovered assets into cache
                    await Promise.allSettled(
                        assets.map(async (asset) => {
                            try {
                                const res = await fetch(asset);
                                if (res.ok) await cache.put(asset, res);
                            } catch { /* best-effort */ }
                        })
                    );
                }
            } catch { /* offline install — cache will populate on first fetch */ }
            await self.skipWaiting();
        })()
    );
});

self.addEventListener('activate', (event) => {
    // Claim all clients immediately
    event.waitUntil(
        Promise.all([
            self.clients.claim(),
            // Clean up old caches (any cache not matching current version prefix+hash)
            caches.keys().then(keys =>
                Promise.all(
                    keys.filter(key => key !== CACHE_NAME && key.startsWith(CACHE_VERSION_PREFIX)).map(key => caches.delete(key))
                )
            ),
        ])
    );
});

self.addEventListener('fetch', (event) => {
    const url = event.request.url;

    // Never cache API calls or WebTransport
    if (isApiRequest(url)) return;

    // Navigation requests: try network first, fall back to cached index.html
    if (isNavigationRequest(event.request)) {
        event.respondWith(
            fetch(event.request)
                .then(response => {
                    // Cache the HTML response
                    const clone = response.clone();
                    caches.open(CACHE_NAME).then(cache => cache.put('/', clone));
                    return response;
                })
                .catch(() => caches.match('/'))
                .then(response => response || new Response('Service Unavailable', { status: 503 }))
        );
        return;
    }

    // App shell assets: cache-first
    if (isAppShellRequest(url)) {
        event.respondWith(
            caches.match(event.request).then(cached => {
                if (cached) return cached;
                return fetch(event.request).then(response => {
                    if (response.ok) {
                        const clone = response.clone();
                        caches.open(CACHE_NAME).then(cache => cache.put(event.request, clone));
                    }
                    return response;
                });
            })
        );
        return;
    }

    // Everything else: network only (don't cache)
});
