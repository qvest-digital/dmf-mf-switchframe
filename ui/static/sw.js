// Service worker for Switchframe SPA resilience.
// Caches the app shell so the page loads even if the engine is unreachable.

const CACHE_NAME = 'switchframe-app-v1';

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

self.addEventListener('install', (event) => {
    // Activate immediately without waiting for old SW to be replaced
    self.skipWaiting();
});

self.addEventListener('activate', (event) => {
    // Claim all clients immediately
    event.waitUntil(
        Promise.all([
            self.clients.claim(),
            // Clean up old caches
            caches.keys().then(keys =>
                Promise.all(
                    keys.filter(key => key !== CACHE_NAME).map(key => caches.delete(key))
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
