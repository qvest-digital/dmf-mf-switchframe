// Dedicated worker: a fetch, an importScripts and a WebSocket to plain
// http/ws on loopback.
const result = {};
try {
    importScripts('http://127.0.0.1:18081/worker-import.js');
    result.worker_import = self.leaked ? 'reached' : 'refused';
} catch (e) {
    result.worker_import = 'refused';
}
Promise.all([
    fetch('http://127.0.0.1:18081/worker-fetch', {mode: 'no-cors'}).then(() => 'reached', () => 'refused'),
    new Promise((r) => {
        const ws = new WebSocket('ws://127.0.0.1:18081/ws-worker');
        ws.onopen = () => r('reached');
        ws.onerror = () => r('refused');
    }),
]).then(([fetched, socket]) => {
    result.worker_fetch = fetched;
    result.ws_worker = socket;
    postMessage(result);
});
