import { wtBaseURL } from '$lib/prism/transport-utils';

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error';

export interface PrismConnectionConfig {
	url: string;
	onControlState: (data: Uint8Array) => void;
	onConnectionChange: (state: ConnectionState) => void;
}

export function createPrismConnection(config: PrismConnectionConfig) {
	let state: ConnectionState = 'disconnected';
	let transport: WebTransport | null = null;
	let retryTimeout: ReturnType<typeof setTimeout> | null = null;
	let retryDelay = 2000;
	let stopped = false;
	const MAX_RETRY_DELAY = 30000;

	function _setConnectionState(newState: ConnectionState) {
		if (newState === state) return;
		state = newState;
		config.onConnectionChange(newState);
	}

	/**
	 * Handle incoming control data from a dedicated MoQ control connection.
	 * In practice, control data arrives via per-source MoQ subscriptions
	 * through the media pipeline (see media-pipeline.ts onControlState).
	 * This remains for the standalone connection fallback path.
	 */
	function _handleControlData(data: Uint8Array) {
		config.onControlState(data);
	}

	async function connect() {
		if (state === 'connecting' || state === 'connected') return;
		stopped = false;
		_setConnectionState('connecting');

		try {
			// Fetch cert info for WebTransport connection
			const certRes = await fetch(`${config.url}/api/cert-hash`);
			const certData = await certRes.json();
			// disconnect() may have landed while we were suspended on the cert
			// fetch — creating a WebTransport now would leak it (nothing would
			// ever close it) and fire a spurious 'connected'.
			if (stopped) return;
			const trusted = certData.trusted === true;

			const opts: WebTransportOptions = {};
			if (!trusted && certData.hash) {
				const hashBinary = atob(certData.hash);
				opts.serverCertificateHashes = [
					{
						algorithm: 'sha-256',
						value: Uint8Array.from(hashBinary, (c) => c.charCodeAt(0)).buffer,
					},
				];
			}

			// The QUIC listener is where the engine advertises it, which is not
			// the page's origin when the page comes through an HTTP proxy.
			const wtUrl = certData.addr
				? wtBaseURL({ certHash: new Uint8Array(), addr: certData.addr, trusted }, new URL(config.url).hostname)
				: config.url;
			transport = new WebTransport(wtUrl, opts);
			const t = transport;
			await t.ready;
			// disconnect() may have landed while we were suspended on
			// transport.ready (it already closed t). Stay torn down rather than
			// reporting 'connected' on a connection the caller abandoned.
			if (stopped) {
				t.close();
				if (transport === t) transport = null;
				return;
			}
			retryDelay = 2000;
			_setConnectionState('connected');

			// Monitor for close
			t.closed
				.then(() => {
					if (stopped) return;
					_setConnectionState('disconnected');
					scheduleRetry();
				})
				.catch(() => {
					if (stopped) return;
					_setConnectionState('error');
					scheduleRetry();
				});
		} catch {
			// An explicit disconnect() during connect (e.g. ConnectionManager
			// teardown) rejects transport.ready and lands here. Reporting
			// 'error' would undo the disconnect: ConnectionManager reacts to
			// 'error' by resurrecting REST polling on a stopped manager.
			if (stopped) return;
			_setConnectionState('error');
			scheduleRetry();
		}
	}

	function scheduleRetry() {
		if (stopped || retryTimeout) return;
		const jitter = retryDelay * 0.2 * Math.random();
		retryTimeout = setTimeout(() => {
			retryTimeout = null;
			retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY);
			connect();
		}, retryDelay + jitter);
	}

	function disconnect() {
		stopped = true;
		if (retryTimeout) {
			clearTimeout(retryTimeout);
			retryTimeout = null;
		}
		transport?.close();
		transport = null;
		_setConnectionState('disconnected');
	}

	return {
		get state() {
			return state;
		},
		get transport() {
			return transport;
		},
		connect,
		disconnect,
		// Test helpers (prefixed with _)
		_handleControlData,
		_setConnectionState,
	};
}
