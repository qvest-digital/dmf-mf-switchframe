// Service worker requests have no browser, so CEF hands them to the
// request context's handler rather than the browser's.
const status = {};
try {
    importScripts('http://127.0.0.1:18081/sw-import.js');
    status.sw_import = self.leaked ? 'reached' : 'refused';
} catch (e) {
    status.sw_import = 'refused';
}

const probe = (path) =>
    fetch('http://127.0.0.1:18081/' + path, {mode: 'no-cors'}).then(() => 'reached', () => 'refused');

self.addEventListener('install', (e) => {
    self.skipWaiting();
    e.waitUntil(probe('sw-install-fetch').then((r) => { status.sw_install_fetch = r; }));
});
self.addEventListener('activate', (e) => e.waitUntil(clients.claim()));
self.addEventListener('fetch', (e) => {
    const path = new URL(e.request.url).pathname;
    if (path.endsWith('/sw-status')) {
        e.respondWith(new Response(JSON.stringify(status)));
    } else if (path.endsWith('/via-sw')) {
        e.respondWith(probe('sw-fetch').then((r) => new Response(r)));
    }
});
