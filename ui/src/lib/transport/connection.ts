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
		_setConnectionState('connecting');

		try {
			// Fetch cert info for WebTransport connection
			const certRes = await fetch(`${config.url}/api/cert-hash`);
			const certData = await certRes.json();
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

			transport = new WebTransport(config.url, opts);
			await transport.ready;
			retryDelay = 2000;
			_setConnectionState('connected');

			// Monitor for close
			transport.closed
				.then(() => {
					_setConnectionState('disconnected');
					scheduleRetry();
				})
				.catch(() => {
					_setConnectionState('error');
					scheduleRetry();
				});
		} catch {
			_setConnectionState('error');
			scheduleRetry();
		}
	}

	function scheduleRetry() {
		if (retryTimeout) return;
		const jitter = retryDelay * 0.2 * Math.random();
		retryTimeout = setTimeout(() => {
			retryTimeout = null;
			retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY);
			connect();
		}, retryDelay + jitter);
	}

	function disconnect() {
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
		connect,
		disconnect,
		// Test helpers (prefixed with _)
		_handleControlData,
		_setConnectionState,
	};
}
