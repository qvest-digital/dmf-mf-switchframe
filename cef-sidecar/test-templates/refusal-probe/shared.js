onconnect = (e) => {
    const port = e.ports[0];
    fetch('http://127.0.0.1:18081/shared-fetch', {mode: 'no-cors'})
        .then(() => 'reached', () => 'refused')
        .then((r) => port.postMessage(r));
};
